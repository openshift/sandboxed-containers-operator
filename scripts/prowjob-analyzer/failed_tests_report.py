#!/usr/bin/env python3
"""
Failed Tests Report Script

Generates detailed reports for failed tests from a Prow job, including
full test logs and failure summaries extracted from build-log.txt.

Usage:
    python3 failed_tests_report.py <PROW_JOB_URL> [TEST_NAME1 TEST_NAME2 ...]
    python3 failed_tests_report.py --json <PROW_JOB_URL> [TEST_NAME1 TEST_NAME2 ...]
"""

import sys
import argparse
import logging
import json
import re
from typing import Dict, List, Optional

# Add lib to path
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'lib'))

from lib.fetcher import (
    parse_prow_url,
    fetch_artifact,
    extract_variant_from_job_name,
)
from lib.parser import (
    parse_prowjob,
    parse_test_results,
    extract_failing_tests,
    parse_test_case_info,
    categorize_test_by_name,
    extract_test_execution_order,
)

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(levelname)s: %(message)s'
)
logger = logging.getLogger(__name__)


def extract_test_logs_from_build_log(build_log_content: str, test_name: str) -> Optional[Dict]:
    """
    Extract full test logs and failure summary for a specific test from build-log.txt.

    Args:
        build_log_content: Content of build-log.txt
        test_name: Name of the test to extract

    Returns:
        Dictionary with full_logs and failure_summary, or None if not found
    """
    lines = build_log_content.split('\n')
    
    # Escape special regex characters in test name
    escaped_test = re.escape(test_name)
    
    # Find start: started: ({stats}) "{test name}"
    start_pattern = rf'started:.*"{escaped_test}"'
    # Find end: failed: ({elapsed time}) {date} "{test name}"
    end_pattern = rf'failed:.*"{escaped_test}"'
    
    start_idx = None
    end_idx = None
    
    for i, line in enumerate(lines):
        if start_idx is None and re.search(start_pattern, line, re.IGNORECASE):
            start_idx = i
            logger.debug(f"Found test start at line {i}: {line[:80]}")
        elif start_idx is not None and re.search(end_pattern, line, re.IGNORECASE):
            end_idx = i
            logger.debug(f"Found test end at line {i}: {line[:80]}")
            break
    
    if start_idx is None:
        logger.warning(f"Could not find start of test logs for: {test_name}")
        return None
    
    if end_idx is None:
        logger.warning(f"Could not find end of test logs for: {test_name}")
        # Use remaining content if we found the start
        end_idx = len(lines) - 1

    # Extract elapsed time from the failed line
    # Pattern: failed: (10m13s) 2025-11-11T00:01:49 "{test name}"
    elapsed_time = "unknown"
    if end_idx is not None and end_idx < len(lines):
        end_line = lines[end_idx]
        elapsed_match = re.search(r'failed:\s*\(([^)]+)\)', end_line, re.IGNORECASE)
        if elapsed_match:
            elapsed_time = elapsed_match.group(1)

    # Extract full test logs
    full_logs_lines = lines[start_idx:end_idx + 1]
    full_logs = '\n'.join(full_logs_lines)

    # Extract failure summary from full logs
    # Find "Summarizing {N} Failure" and extract until end, excluding timestamp lines
    failure_summary_lines = []
    in_summary = False

    for line in full_logs_lines:
        if re.search(r'Summarizing \d+ Failure', line):
            in_summary = True
            continue

        if in_summary:
            # Skip lines that start with timestamp pattern (e.g., "Dec 08 15:20:36.914")
            if re.match(r'^[A-Z][a-z]{2} \d{2} \d{2}:\d{2}:\d{2}\.\d{3}', line):
                continue
            failure_summary_lines.append(line)

    failure_summary = '\n'.join(failure_summary_lines).strip()

    return {
        'full_logs': full_logs,
        'failure_summary': failure_summary,
        'elapsed_time': elapsed_time
    }


def analyze_failed_tests(
    base_url: str,
    variant: str,
    test_names: Optional[List[str]] = None
) -> List[Dict]:
    """
    Analyze failed tests and generate detailed reports.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant
        test_names: Optional list of specific test names to analyze (None = all failed tests)

    Returns:
        List of dictionaries with detailed test information
    """
    results = []
    
    # Fetch test-results.yaml to get list of failed tests
    logger.info("Fetching test results...")
    test_results_path = f"artifacts/{variant}/openshift-extended-test/artifacts/test-results.yaml"
    test_results_content = fetch_artifact(base_url, test_results_path)
    
    if not test_results_content:
        logger.error("Could not fetch test-results.yaml")
        return results
    
    test_results = parse_test_results(test_results_content)
    if not test_results:
        logger.error("Could not parse test-results.yaml")
        return results
    
    # Get list of failed tests
    all_failed_tests = extract_failing_tests(test_results)
    
    if not all_failed_tests:
        logger.info("No failed tests found in test-results.yaml")
        return results
    
    # Filter to specific tests if requested
    if test_names:
        # Match by full name or by test case ID (C#####)
        tests_to_analyze = []
        for test in all_failed_tests:
            test_name = test['name']
            test_case_id = test.get('test_case_number', '')
            
            # Check if this test matches any of the requested names/IDs
            for requested in test_names:
                if requested in test_name or (test_case_id and requested in test_case_id):
                    tests_to_analyze.append(test)
                    break
        
        if not tests_to_analyze:
            logger.warning(f"None of the requested tests found in failed tests list")
            logger.info(f"Available failed tests: {[t['name'] for t in all_failed_tests]}")
            return results
    else:
        tests_to_analyze = all_failed_tests
    
    logger.info(f"Analyzing {len(tests_to_analyze)} failed test(s)...")
    
    # Fetch build-log.txt
    build_log_path = f"artifacts/{variant}/openshift-extended-test/build-log.txt"
    logger.info(f"Fetching build log from {build_log_path}...")
    
    build_log_content = fetch_artifact(base_url, build_log_path)
    if not build_log_content:
        logger.error("Could not fetch build-log.txt")
        return results
    
    build_log_text = build_log_content.decode('utf-8', errors='ignore')
    logger.info(f"Build log size: {len(build_log_text)} characters")
    
    # Process each test
    for test in tests_to_analyze:
        test_name = test['name']
        logger.info(f"Processing test: {test_name}")
        
        # Extract test logs
        test_logs = extract_test_logs_from_build_log(build_log_text, test_name)

        if not test_logs:
            logger.warning(f"Could not extract logs for test: {test_name}")
            test_logs = {'full_logs': '', 'failure_summary': '', 'elapsed_time': 'unknown'}

        # Parse test case info (already done in extract_failing_tests, but get it again)
        test_info = parse_test_case_info(test_name)
        category = categorize_test_by_name(test_name)

        result = {
            'test_name': test_name,
            'test_case_number': test_info['test_case_number'],
            'elapsed_time': test_logs['elapsed_time'],
            'category': category,
            'author': test_info['author'],
            'priority': test_info['priority'],
            'failure_summary': test_logs['failure_summary'],
            'full_logs': test_logs['full_logs'],
            'execution_order': None,  # Will be set below
        }

        results.append(result)

    # Add execution order to each test and sort by it
    # This is critical for root cause analysis - the first test often contains the real error
    execution_order = extract_test_execution_order(build_log_text)
    if execution_order:
        # Create a mapping of test name to execution order number (1-based)
        order_map = {test_name: idx + 1 for idx, test_name in enumerate(execution_order)}

        # Add execution order to each result
        for result in results:
            result['execution_order'] = order_map.get(result['test_name'])

        # Sort results based on execution order
        results.sort(key=lambda r: r['execution_order'] if r['execution_order'] else float('inf'))
        logger.info("Added execution order and sorted test results")
    else:
        logger.warning("Could not extract execution order, tests shown in original order")

    return results


def generate_markdown_report(test_results: List[Dict], base_url: str) -> str:
    """
    Generate markdown report for failed tests.

    Args:
        test_results: List of test analysis results
        base_url: Base URL of the Prow job

    Returns:
        Markdown formatted report
    """
    report = "# Failed Tests Report\n\n"
    report += f"**Job URL**: {base_url}\n\n"
    report += f"**Tests Analyzed**: {len(test_results)}\n\n"
    report += "---\n\n"
    
    for idx, test in enumerate(test_results, 1):
        report += f"## Test {idx}: {test['test_name']}\n\n"
        
        # Metadata
        if test['test_case_number']:
            report += f"- **Test Case ID**: {test['test_case_number']}\n"
        if test.get('execution_order'):
            report += f"- **Execution Order**: {test['execution_order']}\n"
        report += f"- **Elapsed Time**: {test['elapsed_time']}\n"
        report += f"- **Category**: {test['category']}\n"
        if test['author']:
            report += f"- **Author**: {test['author']}\n"
        if test['priority']:
            report += f"- **Priority**: {test['priority']}\n"
        report += "\n"
        
        # Failure summary
        if test['failure_summary']:
            report += "### Failure Summary\n\n"
            report += "```\n"
            report += test['failure_summary']
            report += "\n```\n\n"
        
        # Full logs
        if test['full_logs']:
            report += "### Full Test Logs\n\n"
            report += "```\n"
            report += test['full_logs']
            report += "\n```\n\n"
        
        report += "---\n\n"
    
    return report


def generate_json_report(test_results: List[Dict], base_url: str) -> str:
    """
    Generate JSON report for failed tests.

    Args:
        test_results: List of test analysis results
        base_url: Base URL of the Prow job

    Returns:
        JSON formatted report
    """
    report = {
        'job_url': base_url,
        'tests_analyzed': len(test_results),
        'failed_tests': test_results,
    }
    
    return json.dumps(report, indent=2)


def main():
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description='Generate detailed report for failed tests from a Prow job'
    )
    parser.add_argument('url', help='Prow job URL')
    parser.add_argument('tests', nargs='*', help='Test names or IDs to analyze (omit to analyze all failed tests)')
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
        
        # Analyze failed tests
        test_results = analyze_failed_tests(base_url, variant, args.tests if args.tests else None)
        
        if not test_results:
            logger.error("No test results could be analyzed")
            sys.exit(1)
        
        # Generate report
        if args.json:
            report = generate_json_report(test_results, base_url)
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
