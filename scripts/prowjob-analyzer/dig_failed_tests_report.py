#!/usr/bin/env python3
"""
Failed Tests Report Script (Level 2 analysis)

Generates detailed per-test failure reports for a Prow job's
sandboxed-containers-operator-testsuites chain (osc/kata-upstream/caa).

Unlike the retired OTP (openshift-extended-test) version of this script,
per-test failure detail comes straight from each suite's own JUnit XML
<failure>/<error> text -- confirmed live to already contain the full Ginkgo
assertion/stack-trace output for osc, and the full bats debug output
(including kata-shim logs) for kata-upstream, so there is no shared
build-log.txt to grep a log window out of anymore (each suite has its own,
separate build-log.txt). When a suite step failed but produced no JUnit at
all (the no-results case the prowjob-analyzer plan documents for
osc/kata-upstream), this falls back to a tail of that suite's own
build-log.txt labeled as suite-level, not test-level, detail.

Usage:
    python3 dig_failed_tests_report.py <PROW_JOB_URL> [TEST_NAME1 TEST_NAME2 ...]
    python3 dig_failed_tests_report.py --json <PROW_JOB_URL> [TEST_NAME1 TEST_NAME2 ...]
"""

import sys
import argparse
import logging
import json
from typing import Dict, List

# Add lib to path
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'lib'))

from lib.fetcher import (
    parse_prow_url,
    fetch_artifact,
    extract_variant_from_job_name,
)
from lib.parser import categorize_test_by_name
from lib.failure_analyzer import classify_testsuites_result, analyze_suite_failure

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(levelname)s: %(message)s'
)
logger = logging.getLogger(__name__)

_BUILD_LOG_FALLBACK_TAIL_CHARS = 8000


def collect_all_failing_tests(base_url: str, variant: str) -> List[Dict]:
    """
    Gather JUnit-derived failing-test detail from every FAILED suite in the
    sandboxed-containers-operator-testsuites chain.

    Each entry already carries its own failure_message/system_err (the
    JUnit <failure>/<error> message and full text) -- see
    lib.failure_analyzer.analyze_suite_failure -- plus a suite-level
    build-log.txt tail fallback entry when a suite failed without writing
    any JUnit at all.
    """
    classification = classify_testsuites_result(base_url, variant)
    all_failing: List[Dict] = []

    for suite_key, info in classification.get('suites', {}).items():
        if info['status'] != 'FAILURE':
            continue

        suite_result = analyze_suite_failure(base_url, variant, suite_key, info['step_name'])

        for idx, test in enumerate(suite_result['failing_tests'], 1):
            entry = dict(test)
            entry['suite_order'] = idx
            entry['category'] = categorize_test_by_name(entry.get('name', ''))
            all_failing.append(entry)

        if suite_result['no_results']:
            logger.info(
                f"{info['step_name']} failed with no JUnit at all; "
                "falling back to its build-log.txt tail"
            )
            bl = fetch_artifact(base_url, f"artifacts/{variant}/{info['step_name']}/build-log.txt")
            tail = ''
            if bl:
                text = bl.decode('utf-8', errors='ignore')
                tail = text[-_BUILD_LOG_FALLBACK_TAIL_CHARS:] if len(text) > _BUILD_LOG_FALLBACK_TAIL_CHARS else text
            all_failing.append({
                'name': f"({info['step_name']}: no JUnit produced)",
                'suite': suite_key,
                'junit_file': None,
                'duration': 0,
                'failure_message': 'Suite step failed and produced no JUnit XML at all.',
                'system_out': '',
                'system_err': tail,
                'test_case_number': '',
                'description': f"{info['step_name']} build-log.txt tail (suite-level fallback, not test-level)",
                'priority': '',
                'author': '',
                'category': 'suite_error',
                'suite_order': None,
            })

    return all_failing


def filter_tests(all_failing: List[Dict], requested: List[str]) -> List[Dict]:
    """Match requested names/IDs against a failing test's name or test_case_number."""
    if not requested:
        return all_failing
    matched = []
    for test in all_failing:
        name = test.get('name', '')
        cid = test.get('test_case_number', '')
        for r in requested:
            if r in name or (cid and r in cid):
                matched.append(test)
                break
    return matched


def generate_markdown_report(tests: List[Dict], base_url: str) -> str:
    """Generate markdown report for the given failing tests."""
    report = "# Failed Tests Report\n\n"
    report += f"**Job URL**: {base_url}\n\n"
    report += f"**Tests Analyzed**: {len(tests)}\n\n"
    report += "---\n\n"

    for idx, test in enumerate(tests, 1):
        report += f"## Test {idx}: {test.get('name', 'Unknown')}\n\n"

        if test.get('test_case_number'):
            report += f"- **Test Case ID**: {test['test_case_number']}\n"
        report += f"- **Suite**: {test.get('suite', 'unknown')}\n"
        if test.get('junit_file'):
            report += f"- **JUnit File**: `{test['junit_file']}`\n"
        if test.get('suite_order'):
            report += f"- **Order in Suite's JUnit**: {test['suite_order']}\n"

        duration = test.get('duration') or 0
        if isinstance(duration, (int, float)):
            report += f"- **Duration**: {duration:.1f}s\n"
        else:
            report += f"- **Duration**: {duration}\n"
        report += f"- **Category**: {test.get('category', 'other')}\n\n"

        if test.get('failure_message'):
            report += "### Failure Summary\n\n```\n" + test['failure_message'] + "\n```\n\n"

        if test.get('system_err'):
            heading = (
                "### Suite build-log.txt Tail (fallback, not test-specific)"
                if test.get('category') == 'suite_error'
                else "### Full Failure Detail"
            )
            report += f"{heading}\n\n```\n" + test['system_err'] + "\n```\n\n"

        report += "---\n\n"

    return report


def generate_json_report(tests: List[Dict], base_url: str) -> str:
    """Generate JSON report for the given failing tests."""
    return json.dumps(
        {'job_url': base_url, 'tests_analyzed': len(tests), 'failed_tests': tests},
        indent=2,
    )


def main():
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description=(
            'Generate a detailed per-test failure report for the '
            'sandboxed-containers-operator-testsuites chain (osc/kata-upstream/caa)'
        )
    )
    parser.add_argument('url', help='Prow job URL')
    parser.add_argument(
        'tests', nargs='*',
        help='Test names or IDs to analyze (omit to analyze all failed tests)',
    )
    parser.add_argument('--json', action='store_true', help='Output JSON format instead of markdown')
    parser.add_argument('--verbose', action='store_true', help='Enable verbose logging')

    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    try:
        logger.info("Parsing Prow job URL...")
        base_url, job_name, build_id = parse_prow_url(args.url)
        logger.info(f"Job: {job_name}")
        logger.info(f"Build ID: {build_id}")

        variant = extract_variant_from_job_name(job_name)
        if not variant:
            logger.error("Could not determine job variant from job name")
            sys.exit(1)
        logger.info(f"Variant: {variant}")

        logger.info("Collecting failing tests from the testsuites chain...")
        all_failing = collect_all_failing_tests(base_url, variant)

        if not all_failing:
            logger.info(
                "No failing tests found (suites passed, or the testsuites chain "
                "never ran -- see dig.py for the overall classification)"
            )
            sys.exit(1)

        tests_to_report = filter_tests(all_failing, args.tests)
        if args.tests and not tests_to_report:
            logger.warning("None of the requested tests found in the failing tests list")
            logger.info(f"Available failed tests: {[t.get('name') for t in all_failing]}")
            sys.exit(1)

        logger.info(f"Reporting on {len(tests_to_report)} test(s)...")

        if args.json:
            report = generate_json_report(tests_to_report, base_url)
        else:
            report = generate_markdown_report(tests_to_report, base_url)

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
