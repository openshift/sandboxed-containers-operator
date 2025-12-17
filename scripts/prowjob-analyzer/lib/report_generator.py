"""
Report Generator Module

Generates human-readable markdown and machine-parsable JSON reports.
"""

import json
import logging
from typing import Dict, List, Optional
from datetime import datetime
from .parser import format_duration
from .fetcher import get_artifact_url

logger = logging.getLogger(__name__)


def format_status_emoji(status: str) -> str:
    """Get emoji for job status."""
    status_emojis = {
        'success': '✅',
        'failure': '❌',
        'timeout': '⏱️',
        'error': '💥',
        'aborted': '🛑',
        'pending': '⏳',
        'unknown': '❓',
    }
    return status_emojis.get(status.lower(), '❓')


def generate_test_results_section(test_results: Dict, failure_analysis: Dict) -> str:
    """Generate test results section for report."""
    if not test_results:
        return "\n## Test Results\n\nNo test results available.\n"

    # Handle different test results formats
    total = test_results.get('total', 0)
    if total == 0:
        # Fallback to 'tests' field or count of test list
        total = test_results.get('tests', 0) if isinstance(test_results.get('tests'), int) else len(test_results.get('tests', []))
    failures = test_results.get('failures', 0)
    passed = total - failures
    skipped = test_results.get('skipped', 0)

    section = "\n## Test Results\n\n"
    section += f"- **Total Tests**: {total}\n"
    section += f"- **Passed**: {passed} ✅\n"
    section += f"- **Failed**: {failures} {'❌' if failures > 0 else ''}\n"
    section += f"- **Skipped**: {skipped}\n"

    # Add failing tests breakdown if any
    if failures > 0:
        failing_tests_by_cat = failure_analysis.get('failing_tests_by_category', {})
        if failing_tests_by_cat:
            section += "\n### Failed Tests\n\n"
            for category, tests in sorted(failing_tests_by_cat.items()):
                section += f"#### {category.replace('_', ' ').title()} ({len(tests)} tests)\n\n"
                for test in tests:
                    test_case_num = test.get('test_case_number', '')
                    description = test.get('description', '')
                    test_name = test.get('name', 'Unknown')

                    # Display test case number and description if available
                    if test_case_num and description:
                        section += f"- **{test_case_num}**: {description}\n"
                        # Add priority and author if available
                        if test.get('priority'):
                            section += f"  - Priority: {test['priority']}\n"
                        if test.get('author'):
                            section += f"  - Author: {test['author']}\n"
                    else:
                        # Fallback to showing test name
                        if len(test_name) > 100:
                            test_name = test_name[:97] + "..."
                        section += f"- `{test_name}`\n"
                section += "\n"

    return section


def generate_failure_analysis_section(failure_analysis: Dict, base_url: str, variant: str) -> str:
    """Generate failure analysis section."""
    if not failure_analysis:
        return ""

    section = "\n## Failure Analysis\n\n"

    # Failure location (now shows actual step name(s))
    location = failure_analysis.get('failure_location', 'unknown')
    section += f"**Failed Step(s)**: `{location}`\n\n"

    # Add context based on special statuses
    if location == 'timeout':
        section += "The job exceeded its execution timeout.\n\n"
    elif location == 'infrastructure':
        section += "The job failed due to infrastructure issues.\n\n"
    elif location == 'unknown':
        section += "Could not determine which step failed.\n\n"

    return section


def generate_artifacts_section(base_url: str, variant: str, test_results_available: bool) -> str:
    """Generate artifacts links section."""
    section = "\n## Artifacts\n\n"

    section += f"- [Prow Job]({base_url})\n"
    section += f"- [prowjob.json]({get_artifact_url(base_url, 'prowjob.json')})\n"
    section += f"- [finished.json]({get_artifact_url(base_url, 'finished.json')})\n"

    if variant and variant != 'unknown':
        section += f"- [Test Results]({get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/test-results.yaml')})\n"
        section += f"- [Extended Test Logs]({get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/extended.log')})\n"
        section += f"- [Must-Gather]({get_artifact_url(base_url, f'artifacts/{variant}/sandboxed-containers-operator-gather-must-gather/artifacts/')})\n"

    return section


def generate_human_report(
    prowjob_data: Dict,
    metadata: Dict,
    status: str,
    test_results: Optional[Dict],
    failure_analysis: Optional[Dict],
    base_url: str
) -> str:
    """
    Generate human-readable markdown report.

    Args:
        prowjob_data: Parsed prowjob data
        metadata: Extracted metadata
        status: Overall job status
        test_results: Test results (if available)
        failure_analysis: Failure analysis (if failed)
        base_url: Base URL of Prow job

    Returns:
        Markdown formatted report
    """
    report = "# Prow Job Analysis Report\n\n"

    # Status
    status_emoji = format_status_emoji(status)
    report += f"## Status: {status_emoji} {status.upper()}\n\n"

    # Job Overview
    report += "## Job Overview\n\n"
    report += f"- **Job Name**: `{metadata['job_name']}`\n"
    report += f"- **Snowflake ID**: `{metadata['build_id']}`\n"
    report += f"- **Trigger**: {metadata['trigger_source']}\n"

    # Show release stage if available
    if metadata.get('release_stage'):
        report += f"- **Release Stage**: {metadata['release_stage']}\n"

    duration = prowjob_data.get('duration_seconds', 0)
    if duration > 0:
        report += f"- **Duration**: {format_duration(duration)}\n"

    start_time = prowjob_data.get('start_time', '')
    if start_time:
        report += f"- **Started**: {start_time}\n"

    report += f"- **URL**: {base_url}\n"

    # Environment
    report += "\n## Environment\n\n"
    report += f"- **Provider**: {metadata['provider']}\n"
    report += f"- **OCP Version**: {metadata['ocp_version']}\n"
    report += f"- **OCP Channel**: {metadata.get('ocp_channel', 'unknown')}\n"
    report += f"- **Workload**: {metadata['workload_type']}\n"
    report += f"- **Variant**: {metadata['variant']}\n"

    if metadata.get('kata_rpm_version'):
        rpm_version = metadata['kata_rpm_version']
        rpm_source = metadata.get('kata_rpm_source', 'unknown')
        if rpm_version != 'node-default':
            report += f"- **Kata RPM**: {rpm_version} ({rpm_source})\n"
        else:
            report += f"- **Kata RPM**: Using node default (not installed by job)\n"

    if metadata.get('catalog_source_image'):
        catalog = metadata['catalog_source_image']

        # For digest format (@sha256:...), show only the image name without digest
        if '@' in catalog:
            catalog_display = catalog.split('@')[0]
        else:
            catalog_display = catalog

        # Shorten long catalog images
        if len(catalog_display) > 80:
            catalog_display = "..." + catalog_display[-77:]

        report += f"- **Catalog Image**: `{catalog_display}`\n"

        # Display full tag if available
        if metadata.get('full_tag'):
            report += f"- **Catalog Tag**: `{metadata['full_tag']}`\n"

            # Display base version if available
            if metadata.get('base_version'):
                report += f"- **Catalog Version**: {metadata['base_version']}\n"

            # Always display build date
            build_date = metadata.get('build_date', 'unknown')
            if build_date == 'invalid-timestamp':
                build_date = 'unknown'
            if build_date == 'unknown':
                report += f"- **Catalog Build Date**: Unknown\n"
            else:
                report += f"- **Catalog Build Date**: {build_date}\n"

    # Always show expected operator version, even if empty
    expected_ver = metadata.get('expected_operator_version', '')
    if expected_ver:
        report += f"- **Expected Operator Version**: {expected_ver}\n"
    else:
        report += f"- **Expected Operator Version**: (not set)\n"

    # Test Results
    if test_results:
        report += generate_test_results_section(test_results, failure_analysis or {})

    # Failure Analysis (if failed)
    if status != 'success' and failure_analysis:
        report += generate_failure_analysis_section(failure_analysis, base_url, metadata.get('variant', ''))

    # Artifacts
    report += generate_artifacts_section(base_url, metadata.get('variant', ''), test_results is not None)

    return report


def generate_json_report(
    prowjob_data: Dict,
    metadata: Dict,
    status: str,
    test_results: Optional[Dict],
    failure_analysis: Optional[Dict],
    base_url: str
) -> str:
    """
    Generate machine-parsable JSON report.

    Args:
        prowjob_data: Parsed prowjob data
        metadata: Extracted metadata
        status: Overall job status
        test_results: Test results (if available)
        failure_analysis: Failure analysis (if failed)
        base_url: Base URL of Prow job

    Returns:
        JSON formatted report
    """
    report = {
        'version': '1.0',
        'timestamp': datetime.utcnow().isoformat() + 'Z',
        'prowjob': {
            'url': base_url,
            'name': metadata['job_name'],
            'build_id': metadata['build_id'],
            'status': status,
            'trigger': metadata['trigger_source'],
            'release_stage': metadata.get('release_stage', ''),
            'duration_seconds': prowjob_data.get('duration_seconds', 0),
            'start_time': prowjob_data.get('start_time', ''),
            'completion_time': prowjob_data.get('completion_time', ''),
        },
        'metadata': {
            'provider': metadata['provider'],
            'ocp_version': metadata['ocp_version'],
            'ocp_channel': metadata.get('ocp_channel', 'unknown'),
            'workload_type': metadata['workload_type'],
            'kata_rpm_version': metadata.get('kata_rpm_version', 'unknown'),
            'kata_rpm_source': metadata.get('kata_rpm_source', 'unknown'),
            'variant': metadata['variant'],
            'catalog_source_image': metadata.get('catalog_source_image', ''),
            'catalog_full_tag': metadata.get('full_tag', ''),
            'catalog_version': metadata.get('base_version', ''),
            'catalog_build_date': metadata.get('build_date', ''),
            'catalog_timestamp': metadata.get('timestamp', ''),
            'expected_operator_version': metadata.get('expected_operator_version', ''),
        },
    }

    # Test results summary
    if test_results:
        # Handle different test results formats
        total = test_results.get('total', 0)
        if total == 0:
            # Fallback to 'tests' field or count of test list
            total = test_results.get('tests', 0) if isinstance(test_results.get('tests'), int) else len(test_results.get('tests', []))
        failures = test_results.get('failures', 0)

        report['test_results'] = {
            'total': total,
            'passed': total - failures,
            'failed': failures,
            'skipped': test_results.get('skipped', 0),
        }

    # Failure analysis
    if failure_analysis:
        failing_tests = failure_analysis.get('failing_tests', [])

        report['failure_analysis'] = {
            'failed_steps': failure_analysis.get('failure_location', 'unknown'),
            'failing_tests': [
                {
                    'name': test.get('name', ''),
                    'test_case_number': test.get('test_case_number', ''),
                    'description': test.get('description', ''),
                    'priority': test.get('priority', ''),
                    'author': test.get('author', ''),
                    'duration': test.get('duration', 0),
                }
                for test in failing_tests
            ],
            'detected_patterns': failure_analysis.get('detected_patterns', []),
            'root_cause': failure_analysis.get('root_cause', {}),
        }

    # Artifacts
    variant = metadata.get('variant', '')
    report['artifacts'] = {
        'prowjob_json': get_artifact_url(base_url, 'prowjob.json'),
        'finished_json': get_artifact_url(base_url, 'finished.json'),
    }

    if variant and variant != 'unknown':
        report['artifacts']['test_results'] = get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/test-results.yaml')
        report['artifacts']['extended_log'] = get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/extended.log')
        report['artifacts']['must_gather'] = get_artifact_url(base_url, f'artifacts/{variant}/sandboxed-containers-operator-gather-must-gather/artifacts/')

    return json.dumps(report, indent=2)
