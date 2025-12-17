#!/usr/bin/env python3
"""
Test Report Script

Provides detailed debugging information for specific test cases from a Prow job.
Fetches test details, error messages, and log context to help debug failures.

Usage:
    python3 test_report.py <PROW_JOB_URL> <TEST_NAME1> [TEST_NAME2 ...]
    python3 test_report.py --json <PROW_JOB_URL> <TEST_NAME1> [TEST_NAME2 ...]
"""

import sys
import argparse
import logging
import json
import re
from typing import Dict, List, Optional

# Add lib to path
from lib.fetcher import parse_prow_url, fetch_artifact, extract_variant_from_job_name
from lib.parser import parse_test_results, categorize_test_by_name

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(levelname)s: %(message)s'
)
logger = logging.getLogger(__name__)


def extract_test_details_from_log(log_content: str, test_name: str) -> Dict:
    """
    Extract detailed information about a specific test from build-log.txt.

    Args:
        log_content: Content of the build-log.txt file
        test_name: Name of the test to search for

    Returns:
        Dictionary with test details and debugging information
    """
    details = {
        'test_name': test_name,
        'found': False,
        'error_message': '',
        'log_snippet': '',
        'duration': '',
        'patterns': [],
    }

    # Escape special regex characters in test name
    escaped_test = re.escape(test_name)

    # Search for the test in the log
    # Common pattern: "fail [X.XXs]     TestName"
    test_pattern = rf'(?:fail|FAIL)\s+\[([^\]]+)\]\s+{escaped_test}'
    match = re.search(test_pattern, log_content, re.IGNORECASE)

    if not match:
        # Try without duration
        test_pattern = rf'(?:fail|FAIL).*{escaped_test}'
        match = re.search(test_pattern, log_content, re.IGNORECASE)

    if match:
        details['found'] = True
        if match.lastindex and match.lastindex >= 1:
            details['duration'] = match.group(1)

        # Find position in log
        match_pos = match.start()

        # Extract context around the failure (500 chars before, 2000 chars after)
        start = max(0, match_pos - 500)
        end = min(len(log_content), match_pos + 2000)
        context = log_content[start:end]

        # Look for error messages in the context
        error_lines = []
        for line in context.split('\n'):
            if any(keyword in line.lower() for keyword in ['error:', 'failed:', 'panic:', 'fatal:', 'exception:']):
                error_lines.append(line.strip())

        if error_lines:
            details['error_message'] = '\n'.join(error_lines[:5])  # First 5 error lines

        # Extract a clean log snippet (remove excessive whitespace)
        snippet_lines = [line for line in context.split('\n') if line.strip()]
        details['log_snippet'] = '\n'.join(snippet_lines[:30])  # First 30 non-empty lines

        # Detect common failure patterns
        patterns = []
        context_lower = context.lower()
        if 'timeout' in context_lower or 'deadline exceeded' in context_lower:
            patterns.append('timeout')
        if 'oomkilled' in context_lower or 'out of memory' in context_lower:
            patterns.append('oom')
        if 'connection refused' in context_lower or 'i/o timeout' in context_lower:
            patterns.append('network')
        if 'kata' in context_lower and 'failed' in context_lower:
            patterns.append('kata_failure')
        if 'image pull' in context_lower or 'imagepullbackoff' in context_lower:
            patterns.append('image_pull')

        details['patterns'] = patterns

    return details


def analyze_test_cases(base_url: str, variant: str, test_names: List[str]) -> List[Dict]:
    """
    Analyze specific test cases and gather debugging information.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant for artifact paths
        test_names: List of test names to analyze

    Returns:
        List of dictionaries with test analysis results
    """
    results = []

    # Fetch build-log.txt from extended test artifacts
    build_log_path = f"artifacts/{variant}/openshift-extended-test/build-log.txt"
    logger.info(f"Fetching test logs from {build_log_path}...")

    log_content = fetch_artifact(base_url, build_log_path)
    if not log_content:
        logger.error(f"Failed to fetch build log from {build_log_path}")
        return results

    try:
        log_text = log_content.decode('utf-8', errors='ignore')
    except Exception as e:
        logger.error(f"Failed to decode build log: {e}")
        return results

    logger.info(f"Analyzing {len(test_names)} test case(s)...")

    # Analyze each test
    for test_name in test_names:
        logger.info(f"Processing test: {test_name}")

        details = extract_test_details_from_log(log_text, test_name)

        # Add test categorization
        details['category'] = categorize_test_by_name(test_name)

        results.append(details)

    return results


def generate_markdown_report(test_results: List[Dict], base_url: str) -> str:
    """
    Generate human-readable markdown report for test cases.

    Args:
        test_results: List of test analysis results
        base_url: Base URL of the Prow job

    Returns:
        Markdown formatted report
    """
    report = "# Test Debugging Report\n\n"
    report += f"**Job URL**: {base_url}\n\n"
    report += f"**Tests Analyzed**: {len(test_results)}\n\n"
    report += "---\n\n"

    for idx, test in enumerate(test_results, 1):
        report += f"## Test {idx}: {test['test_name']}\n\n"

        # Test metadata
        report += f"- **Category**: {test['category']}\n"
        if test['duration']:
            report += f"- **Duration**: {test['duration']}\n"

        if test['found']:
            report += f"- **Status**: ❌ Found in logs\n"
        else:
            report += f"- **Status**: ⚠️ Not found in build log\n"

        # Detected patterns
        if test['patterns']:
            report += f"- **Detected Patterns**: {', '.join(test['patterns'])}\n"

        report += "\n"

        # Error message
        if test['error_message']:
            report += "### Error Message\n\n"
            report += "```\n"
            report += test['error_message']
            report += "\n```\n\n"

        # Log snippet
        if test['log_snippet']:
            report += "### Log Context\n\n"
            report += "```\n"
            report += test['log_snippet']
            report += "\n```\n\n"

        # Debugging hints
        if test['patterns']:
            report += "### Debugging Hints\n\n"
            for pattern in test['patterns']:
                if pattern == 'timeout':
                    report += "- **Timeout detected**: Check if resources are sufficient, review slow operations\n"
                elif pattern == 'oom':
                    report += "- **OOM detected**: Increase memory limits or investigate memory leaks\n"
                elif pattern == 'network':
                    report += "- **Network issue detected**: Check connectivity, firewall rules, DNS\n"
                elif pattern == 'kata_failure':
                    report += "- **Kata failure detected**: Check Kata runtime logs, VM configuration\n"
                elif pattern == 'image_pull':
                    report += "- **Image pull issue detected**: Verify image exists, check credentials\n"
            report += "\n"

        if not test['found']:
            report += "### Note\n\n"
            report += "Test not found in build log. This could mean:\n"
            report += "- Test name doesn't match exactly (check spelling/case)\n"
            report += "- Test didn't run or was skipped\n"
            report += "- Test logs are in a different artifact\n\n"

        report += "---\n\n"

    return report


def generate_json_report(test_results: List[Dict], base_url: str) -> Dict:
    """
    Generate JSON report for test cases.

    Args:
        test_results: List of test analysis results
        base_url: Base URL of the Prow job

    Returns:
        Dictionary with test analysis in JSON format
    """
    return {
        'job_url': base_url,
        'tests_analyzed': len(test_results),
        'test_results': test_results,
    }


def main():
    """Main entry point for test report script."""
    parser = argparse.ArgumentParser(
        description='Generate detailed debugging report for specific test cases from a Prow job'
    )
    parser.add_argument('url', help='Prow job URL')
    parser.add_argument('tests', nargs='+', help='Test names to analyze')
    parser.add_argument('--json', action='store_true', help='Output JSON format instead of markdown')
    parser.add_argument('--verbose', action='store_true', help='Enable verbose logging')

    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    try:
        # Parse Prow URL
        logger.info("Parsing Prow job URL...")
        base_url, job_name, build_id = parse_prow_url(args.url)
        logger.info(f"Job: {job_name}")
        logger.info(f"Build ID: {build_id}")

        # Extract variant from job name
        variant = extract_variant_from_job_name(job_name)
        if not variant:
            logger.error("Could not determine job variant from job name")
            sys.exit(1)

        logger.info(f"Variant: {variant}")

        # Analyze test cases
        test_results = analyze_test_cases(base_url, variant, args.tests)

        if not test_results:
            logger.error("No test results could be analyzed")
            sys.exit(1)

        # Generate report
        if args.json:
            report = generate_json_report(test_results, base_url)
            print(json.dumps(report, indent=2))
        else:
            report = generate_markdown_report(test_results, base_url)
            print(report)

    except KeyboardInterrupt:
        logger.info("\nInterrupted by user")
        sys.exit(1)
    except Exception as e:
        logger.error(f"Unexpected error: {e}")
        if args.verbose:
            import traceback
            traceback.print_exc()
        sys.exit(1)


if __name__ == '__main__':
    main()
