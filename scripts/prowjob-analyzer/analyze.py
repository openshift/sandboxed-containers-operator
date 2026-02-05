#!/usr/bin/env python3
"""
Prow Job Analyzer

Analyzes OpenShift Prow job results for OSC testing.
"""

import argparse
import logging
import sys
from typing import Optional

# Add lib directory to path
import os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'lib'))

from lib.fetcher import (
    parse_prow_url,
    fetch_json_artifact,
    fetch_artifact,
    wait_for_artifacts,
    extract_variant_from_job_name,
)
from lib.parser import (
    parse_prowjob,
    parse_test_results,
    get_job_status,
)
from lib.metadata_extractor import extract_metadata
from lib.failure_analyzer import analyze_failure
from lib.report_generator import generate_human_report, generate_json_report, generate_csv_report

# Configure logging
logger = logging.getLogger(__name__)


def setup_logging(verbose: bool = False):
    """Configure logging."""
    level = logging.DEBUG if verbose else logging.INFO
    logging.basicConfig(
        level=level,
        format='%(levelname)s: %(message)s'
    )


def analyze_prowjob(url: str, wait_timeout: int = 300) -> Optional[dict]:
    """
    Analyze a Prow job from its URL.

    Args:
        url: Prow job URL
        wait_timeout: Timeout for waiting for artifacts (seconds)

    Returns:
        Analysis results as dict, or None on error
    """
    try:
        # Parse URL
        logger.info(f"Parsing Prow job URL...")
        base_url, job_name, build_id = parse_prow_url(url)
        logger.info(f"Job: {job_name}")
        logger.info(f"Build ID: {build_id}")

    except ValueError as e:
        logger.error(str(e))
        return None

    # Wait for artifacts if needed
    if wait_timeout > 0:
        logger.info("Checking if artifacts are ready...")
        if not wait_for_artifacts(base_url, timeout=wait_timeout):
            logger.warning("Artifacts not ready yet, attempting analysis anyway...")

    # Fetch prowjob.json
    logger.info("Fetching prowjob.json...")
    prowjob_json = fetch_json_artifact(base_url, "prowjob.json")

    if not prowjob_json:
        logger.error("Failed to fetch prowjob.json - cannot analyze job")
        return None

    # Parse prowjob
    logger.info("Parsing prowjob data...")
    prowjob_data = parse_prowjob(prowjob_json)

    # Extract metadata
    logger.info("Extracting metadata...")
    metadata = extract_metadata(prowjob_data, base_url)
    logger.info(f"Provider: {metadata['provider']}, OCP: {metadata['ocp_version']}, Workload: {metadata['workload_type']}")

    # Fetch test results if available
    logger.info("Fetching test results...")
    test_results = None
    variant = metadata.get('variant')

    if variant and variant != 'unknown':
        test_results_path = f"artifacts/{variant}/openshift-extended-test/artifacts/test-results.yaml"
        test_results_content = fetch_artifact(base_url, test_results_path)

        if test_results_content:
            test_results = parse_test_results(test_results_content)
            if test_results:
                logger.info(f"Test results found: {test_results.get('failures', 0)} failures")
        else:
            logger.warning("test-results.yaml not found (might be a prow step failure)")

    # Determine job status
    status = get_job_status(prowjob_json, test_results)
    logger.info(f"Job status: {status}")

    # Analyze failures if job failed
    failure_analysis = None
    if status != 'success':
        logger.info("Analyzing failure...")
        failure_analysis = analyze_failure(prowjob_data, base_url, test_results, metadata)
        logger.info(f"Failed step(s): {failure_analysis.get('failure_location')}")
        logger.info(f"Failing tests: {len(failure_analysis.get('failing_tests', []))}")
        logger.info(f"Detected patterns: {failure_analysis.get('detected_patterns')}")

    return {
        'prowjob_data': prowjob_data,
        'metadata': metadata,
        'status': status,
        'test_results': test_results,
        'failure_analysis': failure_analysis,
        'base_url': base_url,
    }


def main():
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description='Analyze OpenShift Prow job results for OSC testing',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
Examples:
  %(prog)s https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688

  %(prog)s --json <URL> > report.json

  %(prog)s --csv <URL> >> jobs.csv
  %(prog)s --csv --no-header <URL>

  %(prog)s --verbose --no-wait <URL>
        '''
    )

    parser.add_argument(
        'url',
        help='Prow job URL'
    )

    parser.add_argument(
        '--json',
        action='store_true',
        help='Output machine-readable JSON format'
    )

    parser.add_argument(
        '--csv',
        action='store_true',
        help='Output CSV format (one row: start_time, catalog_full_tag, catalog_build_date, provider, ocp_version, prowjob url, workload_type, kata_rpm_version, status, trigger, root_cause)'
    )

    parser.add_argument(
        '--no-header',
        action='store_true',
        help='Omit CSV header row (only applies with --csv)'
    )

    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Enable verbose logging'
    )

    parser.add_argument(
        '--wait',
        type=int,
        default=300,
        metavar='SECONDS',
        help='Wait timeout for in-progress jobs in seconds (default: 300, 0 to disable)'
    )

    parser.add_argument(
        '--no-wait',
        action='store_true',
        help='Do not wait for in-progress jobs (same as --wait 0)'
    )

    args = parser.parse_args()

    # Setup logging
    setup_logging(args.verbose)

    # Determine wait timeout
    wait_timeout = 0 if args.no_wait else args.wait

    # Analyze job
    results = analyze_prowjob(args.url, wait_timeout=wait_timeout)

    if results is None:
        logger.error("Analysis failed")
        sys.exit(2)

    # Generate report
    if args.json:
        logger.info("Generating JSON report...")
        report = generate_json_report(
            results['prowjob_data'],
            results['metadata'],
            results['status'],
            results['test_results'],
            results['failure_analysis'],
            results['base_url']
        )
    elif args.csv:
        logger.info("Generating CSV report...")
        report = generate_csv_report(
            results['prowjob_data'],
            results['metadata'],
            results['status'],
            results['test_results'],
            results['failure_analysis'],
            results['base_url'],
            header=not args.no_header,
        )
    else:
        logger.info("Generating human-readable report...")
        report = generate_human_report(
            results['prowjob_data'],
            results['metadata'],
            results['status'],
            results['test_results'],
            results['failure_analysis'],
            results['base_url']
        )

    # Output report
    if not args.csv:
        print("\n" + "="*80 + "\n")
    print(report)

    # Exit code based on job status
    if results['status'] == 'success':
        sys.exit(0)
    elif results['status'] in ['failure', 'timeout', 'aborted']:
        sys.exit(1)
    else:
        sys.exit(2)


if __name__ == '__main__':
    main()
