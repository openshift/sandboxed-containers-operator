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
from .parser import format_hours_minutes
from .fetcher import get_artifact_url

logger = logging.getLogger(__name__)

# Human-readable labels for the three-way testsuites-chain failure
# classification (see the prowjob-analyzer plan, Workstreams C/E).
_FAILURE_KIND_LABELS = {
    'blocking_phase_failure': (
        'Blocking phase failure — the pre/test phase failed before the '
        'testsuites chain could run (or its own gate step failed). None of '
        'the suite results below reflect real test coverage for this run.'
    ),
    'best_effort_suite_failure': (
        'Best-effort suite failure — one or more testsuites-chain suites '
        'failed. `best_effort: true` means a failing suite does not block '
        'the others (see Test Suites below for which ones actually failed).'
    ),
    'unrelated_post_step_failure': (
        'Unrelated post-step failure — the testsuites chain itself looks '
        'clean; a different post-phase step (e.g. must-gather, deprovision) '
        'failed independently.'
    ),
}


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


def generate_testsuites_section(suite_summaries: Optional[Dict], failure_kind: Optional[str]) -> str:
    """
    Per-step breakdown of the sandboxed-containers-operator-testsuites chain
    (the test:-phase gate, plus the post:-phase osc/kata-upstream/caa
    suites), shown independently of overall job status -- useful context
    even on a passing job (e.g. "caa produced no JUnit, likely disabled"),
    not just when something failed (see the prowjob-analyzer plan,
    Workstream E). Each step reports its own elapsed time and status in
    addition to (not instead of) the chain-wide `test_elapsed_time`/
    classification summary shown elsewhere in the report.
    """
    if not suite_summaries:
        return ""

    section = "\n## Test Suites\n\n"

    status_emoji = {'SUCCESS': '✅', 'FAILURE': '❌', None: '❓'}

    for suite_key in ('gate', 'osc', 'kata-upstream', 'caa'):
        info = suite_summaries.get(suite_key)
        if not info:
            continue
        emoji = status_emoji.get(info.get('status'), '❓')
        section += f"### {emoji} `{info['step_name']}`\n\n"

        if not info.get('step_exists'):
            section += "Did not run.\n\n"
            continue

        elapsed = info.get('elapsed_time') or 'unknown'

        if suite_key == 'gate':
            section += f"Status: **{info.get('status') or 'unknown'}** — elapsed {elapsed}\n\n"
            continue

        if not info.get('had_junit'):
            section += (
                f"Status: **{info.get('status') or 'unknown'}** — elapsed {elapsed} — "
                "produced no JUnit XML.\n\n"
            )
        else:
            section += (
                f"Status: **{info.get('status') or 'unknown'}** — elapsed {elapsed} — "
                f"{info.get('tests', 0)} tests, {info.get('passed', 0)} passed, "
                f"{info.get('failures', 0)} failed, {info.get('errors', 0)} errors, "
                f"{info.get('skipped', 0)} skipped\n\n"
            )
            if info.get('tests', 0) == 0:
                section += (
                    "(Zero tests reported — likely disabled via "
                    f"`TESTS_{suite_key.upper().replace('-', '_')}_ENABLE=false`, "
                    "or self-skipped because the testsuites-gate marker was missing.)\n\n"
                )

        failing_tests = info.get('failing_tests') or []
        if failing_tests:
            section += "**Failing tests:**\n\n"
            for test in failing_tests:
                case = test.get('test_case_number')
                desc = test.get('description') or test.get('name', 'Unknown')
                label = f"**{case}**: {desc}" if case else f"`{test.get('name', 'Unknown')}`"
                section += f"- {label}\n"
                if test.get('failure_message'):
                    msg = test['failure_message'].strip().splitlines()[0][:200]
                    section += f"  - {msg}\n"
            section += "\n"
        elif info.get('no_results'):
            section += (
                "Step failed and produced no JUnit at all — no per-test detail "
                "available; inspect its build-log.txt directly.\n\n"
            )

    if failure_kind in _FAILURE_KIND_LABELS:
        section += f"> {_FAILURE_KIND_LABELS[failure_kind]}\n\n"

    return section


def generate_failure_analysis_section(failure_analysis: Dict, base_url: str, variant: str) -> str:
    """Generate failure analysis section."""
    if not failure_analysis:
        return ""

    section = "\n## Failure Analysis\n\n"

    failure_kind = failure_analysis.get('failure_kind')
    if failure_kind in _FAILURE_KIND_LABELS:
        section += f"**Classification**: {_FAILURE_KIND_LABELS[failure_kind]}\n\n"

    # Failure location (now shows actual step name(s))
    location = failure_analysis.get('failure_location', 'unknown')
    section += f"**Failed Step(s)**: `{location}`\n\n"

    # Add context based on special statuses (only reachable via the
    # variant-unknown fallback path in analyze_failure -- see identify_failure_location)
    if location == 'timeout':
        section += "The job exceeded its execution timeout.\n\n"
    elif location == 'infrastructure':
        section += "The job failed due to infrastructure issues.\n\n"
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


def generate_artifacts_section(
    base_url: str,
    variant: str,
    test_results_available: bool,
    analyzed_files: List[Dict] = None,
    suite_summaries: Optional[Dict] = None,
) -> str:
    """Generate artifacts links section."""
    section = "\n## Artifacts\n\n"

    # Standard artifacts
    section += f"- [Prow Job]({base_url})\n"
    section += f"- [prowjob.json]({get_artifact_url(base_url, 'prowjob.json')})\n"
    section += f"- [finished.json]({get_artifact_url(base_url, 'finished.json')})\n"

    if variant and variant != 'unknown':
        if suite_summaries:
            for suite_key in ('osc', 'kata-upstream', 'caa'):
                info = suite_summaries.get(suite_key)
                if not info or not info.get('step_exists'):
                    continue
                step_name = info['step_name']
                section += (
                    f"- [{step_name} build-log.txt]"
                    f"({get_artifact_url(base_url, f'artifacts/{variant}/{step_name}/build-log.txt')})\n"
                )
                section += (
                    f"- [{step_name} artifacts/]"
                    f"({get_artifact_url(base_url, f'artifacts/{variant}/{step_name}/artifacts/')})\n"
                )
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
    test_elapsed_time: str = '',
    suite_summaries: Optional[Dict] = None,
) -> Dict:
    """
    Build the canonical report dict (single source of truth).
    JSON dumps it in full; human report reads a subset from this dict.
    """
    prowjob_ds = int(prowjob_data.get('duration_seconds', 0) or 0)
    prowjob_elapsed_time = format_hours_minutes(prowjob_ds) if prowjob_ds > 0 else 'unknown'

    report = {
        'version': '1.0',
        'timestamp': datetime.utcnow().isoformat() + 'Z',
        'test_elapsed_time': test_elapsed_time or 'unknown',
        'prowjob_elapsed_time': prowjob_elapsed_time,
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
                    # Additive (Workstream E): which testsuites-chain suite/JUnit
                    # file this failing test came from.
                    'suite': test.get('suite', ''),
                    'junit_file': test.get('junit_file', ''),
                }
                for test in failing_tests
            ],
            'failing_tests_by_category': failure_analysis.get('failing_tests_by_category', {}),
            'detected_patterns': failure_analysis.get('detected_patterns', []),
            'analyzed_files': failure_analysis.get('analyzed_files', []),
            'root_cause': failure_analysis.get('root_cause', {}),
        }

    # Additive (Workstream E): per-step breakdown (gate + each suite),
    # independent of overall status -- each with its own elapsed_time/status.
    if suite_summaries:
        report['testsuites'] = {
            suite_key: {
                'step_name': info['step_name'],
                'step_exists': info['step_exists'],
                'status': info['status'],
                'elapsed_time': info.get('elapsed_time') or 'unknown',
                'had_junit': info.get('had_junit', False),
                'tests': info.get('tests', 0),
                'passed': info.get('passed', 0),
                'failures': info.get('failures', 0),
                'errors': info.get('errors', 0),
                'skipped': info.get('skipped', 0),
                'no_results': info.get('no_results', False),
                'failing_tests': [
                    {
                        'name': test.get('name', ''),
                        'test_case_number': test.get('test_case_number', ''),
                        'description': test.get('description', ''),
                        'duration': test.get('duration', 0),
                        'failure_message': test.get('failure_message', ''),
                        'junit_file': test.get('junit_file', ''),
                    }
                    for test in (info.get('failing_tests') or [])
                ],
            }
            for suite_key, info in suite_summaries.items()
        }

    # Artifacts
    variant = metadata.get('variant', '')
    report['artifacts'] = {
        'prowjob_json': get_artifact_url(base_url, 'prowjob.json'),
        'finished_json': get_artifact_url(base_url, 'finished.json'),
    }
    if variant and variant != 'unknown' and suite_summaries:
        for suite_key, info in suite_summaries.items():
            if not info.get('step_exists'):
                continue
            step_name = info['step_name']
            report['artifacts'][f'{suite_key}_build_log'] = get_artifact_url(
                base_url, f'artifacts/{variant}/{step_name}/build-log.txt'
            )
        report['artifacts']['must_gather'] = get_artifact_url(
            base_url, f'artifacts/{variant}/sandboxed-containers-operator-gather-must-gather/artifacts/'
        )

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
    test_elapsed_time, failed_steps, primary_pattern, confidence, root_cause.
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
        report_data.get('test_elapsed_time', ''),
        failure_analysis.get('failure_location', ''),
        root_cause.get('primary_pattern', ''),
        root_cause.get('confidence', ''),
        root_cause.get('likely_cause', ''),
    ]
    out = io.StringIO()
    # Single \n line endings; strip any stray leading newlines (avoids a blank first line
    # when appending rows with --no-header or odd csv.writer/CRLF combinations).
    writer = csv.writer(out, lineterminator='\n')
    if header:
        writer.writerow([
            'trigger', 'start_time', 'catalog_full_tag', 'catalog_build_date',
            'provider', 'ocp_version', 'prowjob_url', 'workload_type',
            'kata_rpm_version', 'prowjob_status', 'test_elapsed_time', 'failed_steps',
            'primary_pattern',
            'confidence', 'root_cause',
        ])
    writer.writerow(row)
    return out.getvalue().lstrip('\r\n')


def generate_human_report(report_data: Dict) -> str:
    """
    Generate human-readable markdown report from the canonical report dict.
    """
    prowjob = report_data['prowjob']
    metadata = report_data['metadata']
    status = prowjob['status']
    test_results = report_data.get('test_results')
    failure_analysis = report_data.get('failure_analysis')
    suite_summaries = report_data.get('testsuites')
    base_url = prowjob['url']

    report = "# Prow Job Analysis Report\n\n"

    # Status
    status_emoji = format_status_emoji(status)
    report += f"## Status: {status_emoji} {status.upper()}\n\n"

    # Job Overview (job_name, build_id, trigger live in prowjob in canonical report)
    report += "## Job Overview\n\n"
    report += f"- **Job Name**: `{prowjob.get('name', '')}`\n"
    report += f"- **Snowflake ID**: `{prowjob.get('build_id', '')}`\n"
    report += f"- **Trigger**: {prowjob.get('trigger', '')}\n"

    if prowjob.get('release_stage'):
        report += f"- **Release Stage**: {prowjob['release_stage']}\n"

    start_time = prowjob.get('start_time', '')
    if start_time:
        report += f"- **Started**: {start_time}\n"

    report += f"- **URL**: {base_url}\n"

    report += f"- **test_elapsed_time**: {report_data.get('test_elapsed_time', 'unknown')} "
    report += "(sandboxed-containers-operator-testsuites chain: gate + osc/kata-upstream/caa)\n"
    report += f"- **prowjob_elapsed_time**: {report_data.get('prowjob_elapsed_time', 'unknown')} "
    report += "(entire Prow job wall time)\n"

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

    fa_kind = (failure_analysis or {}).get('failure_kind')
    report += generate_testsuites_section(suite_summaries, fa_kind)

    if status != 'success' and failure_analysis:
        report += generate_failure_analysis_section(failure_analysis, base_url, metadata.get('variant', ''))

    analyzed_files = failure_analysis.get('analyzed_files', []) if failure_analysis else []
    report += generate_artifacts_section(
        base_url, metadata.get('variant', ''), test_results is not None, analyzed_files, suite_summaries
    )

    return report
