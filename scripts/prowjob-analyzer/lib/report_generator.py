"""
Report Generator Module

Generates human-readable markdown, machine-parsable JSON, and CSV reports.
"""

import csv
import io
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
                    execution_order = test.get('execution_order')

                    # Display test case number and description if available
                    if test_case_num and description:
                        section += f"- **{test_case_num}**: {description}\n"
                        # Add execution order, priority and author if available
                        if execution_order:
                            section += f"  - Execution Order: {execution_order}\n"
                        if test.get('priority'):
                            section += f"  - Priority: {test['priority']}\n"
                        if test.get('author'):
                            section += f"  - Author: {test['author']}\n"
                    else:
                        # Fallback to showing test name
                        if len(test_name) > 100:
                            test_name = test_name[:97] + "..."
                        section += f"- `{test_name}`\n"
                        if execution_order:
                            section += f"  - Execution Order: {execution_order}\n"
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
    elif location == 'tests':
        section += "The job failed during the test phase (test-results.yaml available).\n\n"
    elif location == 'unknown':
        section += "Could not determine which step failed.\n\n"

    # Add detected patterns
    detected_patterns = failure_analysis.get('detected_patterns', [])
    if detected_patterns:
        # Handle both old format (list of strings) and new format (list of dicts)
        if detected_patterns and isinstance(detected_patterns[0], dict):
            pattern_display = []
            for pattern_info in detected_patterns:
                pattern_name = pattern_info['pattern']
                source = pattern_info['source']
                pattern_display.append(f"{pattern_name} ({source})")
            section += f"**Detected Patterns**: {', '.join(pattern_display)}\n\n"
        else:
            # Fallback for old format
            section += f"**Detected Patterns**: {', '.join(detected_patterns)}\n\n"

    # Add root cause analysis
    root_cause = failure_analysis.get('root_cause', {})
    if root_cause.get('likely_cause'):
        section += "### Root Cause Analysis\n\n"
        primary_pattern = root_cause.get('primary_pattern', 'unknown')
        pattern_source = root_cause.get('pattern_source')

        if pattern_source:
            section += f"**Primary Pattern**: {primary_pattern} (found in {pattern_source})\n"
        else:
            section += f"**Primary Pattern**: {primary_pattern}\n"

        section += f"**Likely Cause**: {root_cause.get('likely_cause', 'Unknown')}\n"
        section += f"**Confidence**: {root_cause.get('confidence', 'unknown')}\n\n"

        # Add quota details for Azure quota issues
        quota_details = root_cause.get('quota_details')
        if quota_details and quota_details.get('total') is not None:
            section += f"**Quota Utilization**: {quota_details['leased']}/{quota_details['total']} slots occupied ({quota_details['free']} free)\n\n"

        suggested_actions = root_cause.get('suggested_actions', [])
        if suggested_actions:
            section += "**Suggested Actions**:\n"
            for action in suggested_actions:
                section += f"- {action}\n"
            section += "\n"

    return section


def generate_post_test_prow_pipeline_section(
    failure_analysis: Dict, base_url: str, variant: str
) -> str:
    """Section when tests passed but a later Prow step failed."""
    section = "\n## Prow pipeline issue (not a test failure)\n\n"
    steps = failure_analysis.get('post_test_failed_steps') or []
    loc = failure_analysis.get('failure_location', 'unknown')
    if steps:
        step_list = ', '.join(f'`{s}`' for s in steps)
        section += f"**Failed Prow step(s) after tests**: {step_list}\n\n"
    else:
        section += (
            f"**Failed step(s)**: could not be listed from artifacts "
            f"(location hint: `{loc}`).\n\n"
        )
    note = failure_analysis.get('summary_note', '')
    if note:
        section += f"{note}\n\n"
    root_cause = failure_analysis.get('root_cause', {})
    if root_cause.get('likely_cause'):
        section += f"**Details**: {root_cause['likely_cause']}\n\n"
    suggested = root_cause.get('suggested_actions', [])
    if suggested:
        section += "**Suggested actions**:\n"
        for action in suggested:
            section += f"- {action}\n"
        section += "\n"
    if variant and variant != 'unknown' and steps:
        section += "### Step artifact links\n\n"
        for step in steps:
            prefix = f"artifacts/{variant}/{step}/"
            section += f"- [`{step}`]({get_artifact_url(base_url, prefix)})\n"
        section += "\n"
    return section


def generate_artifacts_section(base_url: str, variant: str, test_results_available: bool, analyzed_files: List[Dict] = None) -> str:
    """Generate artifacts links section."""
    section = "\n## Artifacts\n\n"

    # Standard artifacts
    section += f"- [Prow Job]({base_url})\n"
    section += f"- [prowjob.json]({get_artifact_url(base_url, 'prowjob.json')})\n"
    section += f"- [finished.json]({get_artifact_url(base_url, 'finished.json')})\n"

    if variant and variant != 'unknown':
        section += f"- [Test Results]({get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/test-results.yaml')})\n"
        section += f"- [Extended Test Logs]({get_artifact_url(base_url, f'artifacts/{variant}/openshift-extended-test/artifacts/extended.log')})\n"
        section += f"- [Must-Gather]({get_artifact_url(base_url, f'artifacts/{variant}/sandboxed-containers-operator-gather-must-gather/artifacts/')})\n"

    # Add section for files analyzed for pattern detection
    if analyzed_files:
        section += f"\n### Files Analyzed for Pattern Detection\n\n"
        for file_info in analyzed_files:
            file_name = file_info['name']
            file_path = file_info['path']
            patterns_found = file_info['patterns_found']
            file_url = get_artifact_url(base_url, file_path)

            if patterns_found > 0:
                section += f"- [{file_name}]({file_url}) - **{patterns_found} pattern(s) detected** ⚠️\n"
            else:
                section += f"- [{file_name}]({file_url}) - no patterns detected\n"

    return section


def build_report_data(
    prowjob_data: Dict,
    metadata: Dict,
    status: str,
    test_results: Optional[Dict],
    failure_analysis: Optional[Dict],
    base_url: str,
) -> Dict:
    """
    Build the canonical report dict (single source of truth).
    JSON dumps it in full; human report reads a subset from this dict.
    """
    report = {
        'version': '1.0',
        'timestamp': datetime.utcnow().isoformat() + 'Z',
        'prowjob': {
            'url': base_url,
            'name': metadata['job_name'],
            'build_id': metadata['build_id'],
            'status': status,
            'prow_reported_state': prowjob_data.get('state', 'unknown'),
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
            'catalog_build_date': metadata.get('catalog_build_date', ''),
            'catalog_timestamp': metadata.get('timestamp', ''),
            'expected_operator_version': metadata.get('expected_operator_version', ''),
        },
    }

    # Test results summary
    if test_results:
        total = test_results.get('total', 0)
        if total == 0:
            total = test_results.get('tests', 0) if isinstance(test_results.get('tests'), int) else len(test_results.get('tests', []))
        failures = test_results.get('failures', 0)
        report['test_results'] = {
            'total': total,
            'passed': total - failures,
            'failures': failures,
            'errors': test_results.get('errors', 0),
            'skipped': test_results.get('skipped', 0),
        }

    # Failure analysis (include keys needed for human report subset)
    if failure_analysis:
        failing_tests = failure_analysis.get('failing_tests', [])
        report['failure_analysis'] = {
            'failure_kind': failure_analysis.get('failure_kind'),
            'post_test_failed_steps': failure_analysis.get('post_test_failed_steps'),
            'failure_location': failure_analysis.get('failure_location', 'unknown'),
            'failing_tests': [
                {
                    'name': test.get('name', ''),
                    'test_case_number': test.get('test_case_number', ''),
                    'description': test.get('description', ''),
                    'priority': test.get('priority', ''),
                    'author': test.get('author', ''),
                    'duration': test.get('duration', 0),
                    'execution_order': test.get('execution_order'),
                }
                for test in failing_tests
            ],
            'failing_tests_by_category': failure_analysis.get('failing_tests_by_category', {}),
            'detected_patterns': failure_analysis.get('detected_patterns', []),
            'analyzed_files': failure_analysis.get('analyzed_files', []),
            'root_cause': failure_analysis.get('root_cause', {}),
        }

    if failure_analysis and failure_analysis.get('failure_kind') == 'prow_pipeline_after_tests':
        report['prow_pipeline_issue'] = {
            'failed_steps': failure_analysis.get('post_test_failed_steps') or [],
            'failure_location': failure_analysis.get('failure_location', ''),
            'summary': failure_analysis.get('summary_note', ''),
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

    # Full root_cause dict at top level of canonical report
    report['root_cause'] = (failure_analysis.get('root_cause', {}) if failure_analysis else {})
    return report


def generate_json_report(report_data: Dict) -> str:
    """Serialize the canonical report dict to JSON."""
    return json.dumps(report_data, indent=2)


def generate_csv_report(report_data: Dict, header: bool = True) -> str:
    """
    Generate one row of CSV from the canonical report_data.
    Columns: trigger, start_time, catalog_full_tag, catalog_build_date, provider,
    ocp_version, prowjob_url, workload_type, kata_rpm_version, prowjob_status,
    failed_steps, primary_pattern, confidence, root_cause.
    """
    prowjob = report_data['prowjob']
    metadata = report_data['metadata']
    failure_analysis = report_data.get('failure_analysis') or {}
    root_cause = report_data.get('root_cause') or {}

    catalog_full_tag = (metadata.get('catalog_full_tag') or '').strip()
    if catalog_full_tag.startswith('sha256:'):
        catalog_full_tag = catalog_full_tag[7:]

    row = [
        prowjob.get('trigger', ''),
        prowjob.get('start_time', ''),
        catalog_full_tag,
        metadata.get('catalog_build_date', ''),
        metadata.get('provider', ''),
        metadata.get('ocp_version', ''),
        prowjob.get('url', ''),
        metadata.get('workload_type', ''),
        metadata.get('kata_rpm_version', ''),
        (prowjob.get('status') or '').upper(),
        failure_analysis.get('failure_location', ''),
        root_cause.get('primary_pattern', ''),
        root_cause.get('confidence', ''),
        root_cause.get('likely_cause', ''),
    ]
    out = io.StringIO()
    writer = csv.writer(out)
    if header:
        writer.writerow([
            'trigger', 'start_time', 'catalog_full_tag', 'catalog_build_date',
            'provider', 'ocp_version', 'prowjob_url', 'workload_type',
            'kata_rpm_version', 'prowjob_status', 'failed_steps', 'primary_pattern',
            'confidence', 'root_cause',
        ])
    writer.writerow(row)
    return out.getvalue()


def generate_human_report(report_data: Dict) -> str:
    """
    Generate human-readable markdown report from the canonical repor dict.
    """
    prowjob = report_data['prowjob']
    metadata = report_data['metadata']
    status = prowjob['status']
    test_results = report_data.get('test_results')
    failure_analysis = report_data.get('failure_analysis')
    base_url = prowjob['url']

    report = "# Prow Job Analysis Report\n\n"

    # Status (tests can be SUCCESS while Prow still reports a later pipeline failure)
    status_emoji = format_status_emoji(status)
    report += f"## Status: {status_emoji} {status.upper()}\n\n"
    if report_data.get('prow_pipeline_issue'):
        report += (
            "> **Note:** Extended tests passed. Prow reported a failure in a **later pipeline step** "
            "(not a failing test case). See [Prow pipeline issue](#prow-pipeline-issue-not-a-test-failure) below.\n\n"
        )

    # Job Overview (job_name, build_id, trigger live in prowjob in canonical report)
    report += "## Job Overview\n\n"
    report += f"- **Job Name**: `{prowjob.get('name', '')}`\n"
    report += f"- **Snowflake ID**: `{prowjob.get('build_id', '')}`\n"
    report += f"- **Trigger**: {prowjob.get('trigger', '')}\n"

    if prowjob.get('release_stage'):
        report += f"- **Release Stage**: {prowjob['release_stage']}\n"

    duration = prowjob.get('duration_seconds', 0)
    if duration > 0:
        report += f"- **Duration**: {format_duration(duration)}\n"

    start_time = prowjob.get('start_time', '')
    if start_time:
        report += f"- **Started**: {start_time}\n"

    report += f"- **URL**: {base_url}\n"

    root_cause = report_data.get('root_cause') or {}
    if root_cause.get('likely_cause'):
        report += f"- **Root cause**: {root_cause['likely_cause']}\n"

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
        if '@' in catalog:
            catalog_display = catalog.split('@')[0]
        else:
            catalog_display = catalog
        if len(catalog_display) > 80:
            catalog_display = "..." + catalog_display[-77:]
        report += f"- **Catalog Image**: `{catalog_display}`\n"
        if metadata.get('full_tag'):
            report += f"- **Catalog Tag**: `{metadata['full_tag']}`\n"
            if metadata.get('base_version'):
                report += f"- **Catalog Version**: {metadata['base_version']}\n"
        # Show catalog_build_date as-is so problem values are visible
        build_date = metadata.get('catalog_build_date', 'unknown')
        report += f"- **Catalog Build Date**: {build_date}\n"
    else:
        report += "- **Catalog**: Not set (job may not be an OSC test job)\n"

    expected_ver = metadata.get('expected_operator_version', '')
    if expected_ver:
        report += f"- **Expected Operator Version**: {expected_ver}\n"
    else:
        report += f"- **Expected Operator Version**: (not set)\n"

    if test_results:
        report += generate_test_results_section(test_results, failure_analysis or {})

    fa_kind = (failure_analysis or {}).get('failure_kind')
    if failure_analysis and fa_kind == 'prow_pipeline_after_tests':
        report += generate_post_test_prow_pipeline_section(
            failure_analysis, base_url, metadata.get('variant', '')
        )
    elif status != 'success' and failure_analysis:
        report += generate_failure_analysis_section(failure_analysis, base_url, metadata.get('variant', ''))

    analyzed_files = failure_analysis.get('analyzed_files', []) if failure_analysis else []
    report += generate_artifacts_section(base_url, metadata.get('variant', ''), test_results is not None, analyzed_files)

    return report
