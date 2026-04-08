#!/usr/bin/env python3
"""
Prow Job Analyzer

Analyzes OpenShift Prow job results for OSC testing.
"""

import argparse
import logging
import sys
from pathlib import Path
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
    compute_openshift_extended_test_elapsed_display,
    configure_artifact_source,
    download_job_artifacts,
    write_job_artifacts_tarball,
    is_local_artifact_mode,
)
from lib.parser import (
    parse_prowjob,
    parse_test_results,
    get_job_status,
)
from lib.metadata_extractor import extract_metadata
from lib.failure_analyzer import analyze_failure, analyze_post_test_prow_failure
from lib.report_generator import build_report_data, generate_csv_report, generate_human_report, generate_json_report

# Configure logging
logger = logging.getLogger(__name__)


def setup_logging(verbose: bool = False):
    """Configure logging (always to stderr so stdout stays clean for --csv / redirects)."""
    level = logging.DEBUG if verbose else logging.INFO
    kwargs = {
        'level': level,
        'format': '%(levelname)s: %(message)s',
        'stream': sys.stderr,
    }
    if sys.version_info >= (3, 8):
        kwargs['force'] = True
    logging.basicConfig(**kwargs)


_LOCAL_ONLY_FETCH_BASE = 'local://artifact-bundle'


def analyze_prowjob(
    url: Optional[str] = None,
    wait_timeout: int = 300,
    artifact_source: Optional[str] = None,
) -> Optional[dict]:
    """
    Analyze a Prow job from its URL and/or local artifact tree.

    Args:
        url: Prow job URL (optional if ``artifact_source`` is a directory or ``.tar.gz``
            containing ``prowjob.json``; links in reports use ``status.url`` from that file
            or a ``file://`` URI for the bundle path).
        wait_timeout: Timeout for waiting for artifacts (seconds)
        artifact_source: Optional path to a local directory or ``.tar.gz`` whose layout
            matches Prow (``prowjob.json`` at job root). When set (including after
            ``gsutil`` download via ``--download-artifacts``), all artifact reads use
            this tree only—no per-file HTTP fetches.

    Returns:
        Analysis results as dict, or None on error
    """
    try:
        configure_artifact_source(artifact_source)
    except (OSError, ValueError) as e:
        logger.error("%s", e)
        return None

    if url:
        try:
            logger.info("Parsing Prow job URL...")
            base_url, job_name, build_id = parse_prow_url(url)
            logger.info(f"Job: {job_name}")
            logger.info(f"Build ID: {build_id}")
        except ValueError as e:
            logger.error(str(e))
            return None
        fetch_base = base_url
    else:
        if not is_local_artifact_mode():
            logger.error(
                "Provide a Prow job URL or --artifacts PATH (directory or .tar.gz)."
            )
            return None
        logger.info("Analyzing from local artifacts only (no Prow URL)")
        fetch_base = _LOCAL_ONLY_FETCH_BASE
        base_url = ''

    # Wait for remote artifacts only when not using a local tree (dir or extracted tar).
    if wait_timeout > 0 and not is_local_artifact_mode():
        logger.info("Checking if artifacts are ready...")
        if not wait_for_artifacts(base_url, timeout=wait_timeout):
            logger.warning("Artifacts not ready yet, attempting analysis anyway...")

    # Fetch prowjob.json
    logger.info("Fetching prowjob.json...")
    prowjob_json = fetch_json_artifact(fetch_base, "prowjob.json")

    if not prowjob_json:
        logger.error("Failed to fetch prowjob.json - cannot analyze job")
        return None

    # Parse prowjob
    logger.info("Parsing prowjob data...")
    prowjob_data = parse_prowjob(prowjob_json)

    if not url:
        su = (prowjob_data.get('url') or '').strip()
        if su:
            base_url = su
        elif artifact_source:
            base_url = Path(
                os.path.abspath(os.path.expanduser(artifact_source))
            ).as_uri()
        else:
            base_url = _LOCAL_ONLY_FETCH_BASE
        logger.info(f"Job: {prowjob_data.get('job_name', 'unknown')}")
        logger.info(f"Build ID: {prowjob_data.get('build_id', 'unknown')}")

    # Extract metadata
    logger.info("Extracting metadata...")
    metadata = extract_metadata(prowjob_data, base_url)
    logger.info(f"Provider: {metadata['provider']}, OCP: {metadata['ocp_version']}, Workload: {metadata['workload_type']}")

    # Fetch test results if available
    logger.info("Fetching test results...")
    test_results = None
    variant = metadata.get('variant')

    extended_finished = None
    if variant and variant != 'unknown':
        prefix = f"artifacts/{variant}/openshift-extended-test"
        test_results_path = f"{prefix}/artifacts/test-results.yaml"
        test_results_content = fetch_artifact(base_url, test_results_path)

        if test_results_content:
            test_results = parse_test_results(test_results_content)
            if test_results:
                logger.info(f"Test results found: {test_results.get('failures', 0)} failures")
        else:
            logger.warning("test-results.yaml not found (might be a prow step failure)")

        finished_path = f"{prefix}/finished.json"
        extended_finished = fetch_json_artifact(base_url, finished_path)
        if extended_finished:
            logger.info(
                f"openshift-extended-test finished.json: result={extended_finished.get('result')!r}"
            )

    test_elapsed_time = 'unknown'
    if variant and variant != 'unknown':
        test_elapsed_time = compute_openshift_extended_test_elapsed_display(
            base_url, variant, extended_finished
        )
        if not test_elapsed_time:
            test_elapsed_time = 'unknown'
        logger.info(f"test_elapsed_time: {test_elapsed_time}")

    # Determine job status (test-results primary, then finished.json, then prowjob.json)
    status = get_job_status(prowjob_json, test_results, extended_finished)
    logger.info(f"Job status: {status}")

    # Analyze failures if tests/job failed (test failures)
    failure_analysis = None
    if status != 'success':
        logger.info("Analyzing failure...")
        failure_analysis = analyze_failure(prowjob_data, base_url, test_results, metadata)
        logger.info(f"Failed step(s): {failure_analysis.get('failure_location')}")
        logger.info(f"Failing tests: {len(failure_analysis.get('failing_tests', []))}")
        logger.info(f"Detected patterns: {failure_analysis.get('detected_patterns')}")

    # Tests passed but overall Prow job failed (e.g. later step) — not a test failure
    if status == 'success':
        prow_state = (prowjob_json.get('status') or {}).get('state', '').lower()
        if prow_state not in ('success', 'pending', ''):
            post = analyze_post_test_prow_failure(prowjob_json, base_url, metadata)
            if post:
                failure_analysis = post
                logger.info(
                    "Prow reported non-success after successful tests; "
                    f"post-test step issue: {post.get('failure_location')}"
                )

    return {
        'prowjob_data': prowjob_data,
        'metadata': metadata,
        'status': status,
        'test_results': test_results,
        'failure_analysis': failure_analysis,
        'base_url': base_url,
        'test_elapsed_time': test_elapsed_time,
    }


def main():
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description='Analyze OpenShift Prow job results for OSC testing',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
Examples:
  %(prog)s https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688

  %(prog)s --artifacts /path/to/job-artifacts.tar.gz

  %(prog)s --artifacts /path/to/job-artifacts-dir <URL>

  %(prog)s --download-artifacts . <URL>

  %(prog)s --download-artifacts /tmp --tar-artifacts <URL>

  %(prog)s --json <URL> > report.json

  %(prog)s --verbose --no-wait <URL>

Artifact layout:
  The Prow job Artifacts page lists a gsutil command to download the GCS prefix into a
  local directory. That tree (prowjob.json at the root, artifacts/...) is the same layout
  expected by --artifacts. Use --download-artifacts to run gsutil from this tool (requires
  gsutil on PATH); otherwise run the gsutil command from the UI and pass the directory with
  --artifacts.
        '''
    )

    parser.add_argument(
        'url',
        nargs='?',
        default=None,
        help=(
            'Prow job URL (optional if --artifacts is a directory or .tar.gz containing '
            'prowjob.json)'
        ),
    )

    parser.add_argument(
        '--artifacts',
        metavar='PATH',
        help=(
            'Local directory or .tar.gz of job artifacts (prowjob.json at root). '
            'Same layout as a directory produced by the gsutil command on the Prow '
            'Artifacts page. Reads are cached in memory. May be used alone (no URL) '
            'or with a URL for explicit job identity and links.'
        ),
    )

    parser.add_argument(
        '--download-artifacts',
        nargs='?',
        const='.',
        default=None,
        metavar='PARENT_DIR',
        help=(
            'Run gsutil -m cp -r gs://... (derived like the Prow Artifacts page) into '
            'PARENT_DIR/<build-id> (default parent: current directory). Requires gsutil '
            'on PATH. Analysis uses this folder unless --artifacts is set.'
        ),
    )

    parser.add_argument(
        '--tar-artifacts',
        action='store_true',
        help=(
            'With --download-artifacts, also write PARENT_DIR/<build-id>.tar.gz '
            'next to the downloaded directory.'
        ),
    )

    parser.add_argument(
        '--json',
        action='store_true',
        help='Output machine-readable JSON format'
    )

    parser.add_argument(
        '--csv',
        action='store_true',
        help='Output one row of CSV (from canonical report)'
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

    if args.tar_artifacts and args.download_artifacts is None:
        parser.error('--tar-artifacts requires --download-artifacts')

    if not args.url and not args.artifacts:
        parser.error(
            'Provide a Prow job URL or --artifacts PATH (directory or .tar.gz)'
        )

    if args.download_artifacts is not None and not args.url:
        parser.error('--download-artifacts requires a Prow job URL')

    # Setup logging
    setup_logging(args.verbose)
    logger = logging.getLogger(__name__)

    # Determine wait timeout
    wait_timeout = 0 if args.no_wait else args.wait

    base_url = None
    job_name = None
    build_id = None
    if args.url:
        try:
            base_url, job_name, build_id = parse_prow_url(args.url)
        except ValueError as e:
            logger.error(str(e))
            sys.exit(2)

    downloaded_dir = None
    if args.download_artifacts is not None:
        parent_dir = args.download_artifacts
        if wait_timeout > 0:
            logger.info('Checking if artifacts are ready before download...')
            if not wait_for_artifacts(base_url, timeout=wait_timeout):
                logger.warning(
                    'Artifacts may be incomplete; continuing with download anyway.'
                )
        try:
            downloaded_dir = download_job_artifacts(base_url, parent_dir, build_id)
        except (OSError, ValueError, RuntimeError) as e:
            logger.error('Download failed: %s', e)
            sys.exit(2)
        if args.tar_artifacts:
            tar_path = os.path.join(
                os.path.abspath(os.path.expanduser(parent_dir)),
                f'{build_id}.tar.gz',
            )
            try:
                write_job_artifacts_tarball(downloaded_dir, tar_path)
            except OSError as e:
                logger.error('Tarball failed: %s', e)
                sys.exit(2)

    artifact_source = args.artifacts if args.artifacts else downloaded_dir
    use_local_tree = bool(args.artifacts or downloaded_dir)

    # Analyze: no remote wait when a directory/tar.gz or fresh download supplies artifacts
    results = analyze_prowjob(
        url=args.url,
        wait_timeout=0 if use_local_tree else wait_timeout,
        artifact_source=artifact_source,
    )

    if results is None:
        logger.error("Analysis failed")
        sys.exit(2)

    # Build canonical report once, then output in requested format
    report_data = build_report_data(
        results['prowjob_data'],
        results['metadata'],
        results['status'],
        results['test_results'],
        results['failure_analysis'],
        results['base_url'],
        results.get('test_elapsed_time', ''),
    )
    if args.json:
        logger.info("Generating JSON report...")
        report = generate_json_report(report_data)
    elif args.csv:
        logger.info("Generating CSV report...")
        report = generate_csv_report(report_data, header=not args.no_header)
    else:
        logger.info("Generating human-readable report...")
        report = generate_human_report(report_data)

    # Output report (no separator line for CSV). CSV rows already end with a line
    # terminator from csv.writer; avoid print()'s extra newline (blank line when appending).
    if not args.csv:
        print("\n" + "="*80 + "\n")
        print(report)
    else:
        print(report, end="")

    # Exit code based on job status
    if results['status'] == 'success':
        sys.exit(0)
    elif results['status'] in ['failure', 'timeout', 'aborted']:
        sys.exit(1)
    else:
        sys.exit(2)


if __name__ == '__main__':
    main()
