"""
Failure Analyzer Module

Analyzes failures to identify location, patterns, and root causes.
"""

import re
import logging
from typing import Dict, List, Optional
from collections import defaultdict
from .parser import categorize_test_by_name, extract_failing_tests, add_execution_order_to_tests
from .fetcher import fetch_artifact, get_failed_steps, is_openshift_extended_test_failure_label

logger = logging.getLogger(__name__)

# Common failure patterns
FAILURE_PATTERNS = {
    'timeout': [
        r'context deadline exceeded',
        r'timeout waiting for',
        r'operation timed out',
        r'i/o timeout',
        r'timed out after',
    ],
    'oom': [
        r'OOMKilled',
        r'out of memory',
        r'cannot allocate memory',
        r'memory limit exceeded',
    ],
    'network': [
        r'connection refused',
        r'network unreachable',
        r'no route to host',
        r'connection reset',
        r'dial tcp.*: i/o timeout',
    ],
    'image_pull': [
        r'ImagePullBackOff',
        r'ErrImagePull',
        r'failed to pull image',
        r'manifest unknown',
    ],
    'quota': [
        r'quota exceeded',
        r'insufficient quota',
        r'resource quota',
        r'limit range',
    ],
    'infrastructure': [
        r'cluster.*not ready',
        r'node.*not ready',
        r'failed to provision',
        r'cluster installation failed',
    ],
    'azure_quota': [
        r'failed to acquire lease for.*azure.*quota.*slice',
        r'current capacity: 0 free.*leased',
        r'resources not found.*azure.*quota',
        r'failed to acquire resource.*azure',
        r'azure.*quota.*exhausted',
        r'azure.*capacity.*exceeded',
    ],
    'kata_init': [
        r'kata.*failed to start',
        r'runtime kata.*error',
        r'qemu.*failed',
        r'failed to create sandbox',
    ],
    'rpm_install': [
        r'error:.*Failed dependencies',
        r'error:.*package.*is needed by kata',
        r'rpm.*installation failed',
        r'error:.*nothing provides.*needed by kata',
    ],
    'rpm_cascading': [
        r'error:.*Deployment is already in unlocked state',
        r'error:.*ostree.*unlocked state',
    ],
}


def identify_failure_location(
    prowjob_data: Dict,
    test_results: Optional[Dict],
    base_url: str,
    variant: str
) -> str:
    """
    Identify which step(s) failed in the job.

    Logic:
    - If test-results.yaml doesn't exist: tests didn't run, job failed before tests
    - If test-results.yaml exists with failures > 0: job failed at test execution
    - We rely on finished.json in each step's artifacts to determine which steps failed

    Args:
        prowjob_data: Parsed prowjob data
        test_results: Parsed test results (if available)
        base_url: Base URL of Prow job
        variant: Job variant

    Returns:
        Name of failed step(s), comma-separated if multiple, or status like 'timeout'/'unknown'
    """
    state = prowjob_data.get('state', '').lower()

    # Check if job timed out
    if 'timeout' in state or state == 'aborted':
        return 'timeout'

    # Check if it's a Prow infrastructure failure
    if state in ['error', 'errored']:
        return 'infrastructure'

    # Get actual failed steps from artifacts by checking each step's finished.json
    if variant and variant != 'unknown':
        failed_steps = get_failed_steps(base_url, variant)

        if failed_steps:
            # Return only the steps that actually failed
            return ', '.join(failed_steps)
        else:
            # No failed steps detected from artifacts
            # This can happen if we can't parse the artifacts properly
            if state == 'failure':
                logger.warning("Job failed but couldn't detect failed steps from artifacts")
                return 'unknown'

    # If variant is unknown, we can't check step artifacts
    return 'unknown'


def analyze_post_test_prow_failure(
    prowjob_raw: Dict,
    base_url: str,
    metadata: Dict,
) -> Optional[Dict]:
    """
    When openshift-extended-test succeeded (caller already set status to success)
    but the overall Prow job did not succeed, summarize failed steps after the test phase.

    These are Prow/pipeline issues, not test-case failures.
    """
    status_block = prowjob_raw.get('status') or {}
    prow_state = (status_block.get('state') or '').lower()
    if prow_state in ('success', 'pending', ''):
        return None

    variant = metadata.get('variant') or ''
    failed_steps: List[str] = []
    if variant and variant != 'unknown':
        failed_steps = get_failed_steps(base_url, variant)

    # Defensive: extended test phase is already known-good from test-results/finished.json
    failed_steps = [s for s in failed_steps if not is_openshift_extended_test_failure_label(s)]

    summary_note = (
        'One or more Prow steps failed after openshift-extended-test completed successfully. '
        'This is not a test-case failure.'
    )

    if not failed_steps:
        return {
            'failure_kind': 'prow_pipeline_after_tests',
            'failure_location': 'prowjob',
            'post_test_failed_steps': [],
            'failing_tests': [],
            'failing_tests_by_category': {},
            'detected_patterns': [],
            'analyzed_files': [],
            'root_cause': {
                'likely_cause': (
                    f'Prow job state is {prow_state!r} while the extended test phase reported success. '
                    'A later pipeline step may have failed, or step-level artifacts could not be enumerated.'
                ),
                'confidence': 'medium',
                'suggested_actions': [
                    'Review the Prow UI step timeline and build-log.txt for the failing step.',
                    'Confirm whether a post-test step (e.g. gather, teardown) failed.',
                ],
            },
            'summary_note': summary_note,
        }

    return {
        'failure_kind': 'prow_pipeline_after_tests',
        'failure_location': ', '.join(failed_steps),
        'post_test_failed_steps': failed_steps,
        'failing_tests': [],
        'failing_tests_by_category': {},
        'detected_patterns': [],
        'analyzed_files': [],
        'root_cause': {
            'likely_cause': summary_note,
            'confidence': 'high',
            'suggested_actions': [
                f'Inspect finished.json and logs under artifacts for: {", ".join(failed_steps)}.',
                'This does not indicate failing OSC extended tests.',
            ],
        },
        'summary_note': summary_note,
    }


def extract_azure_quota_details(log_content: str) -> Dict[str, Optional[int]]:
    """
    Extract Azure quota capacity details from log content.

    Args:
        log_content: Log file content as string

    Returns:
        Dictionary with quota details: {free: int, leased: int, total: int}
    """
    quota_details = {'free': None, 'leased': None, 'total': None}

    # Pattern: "current capacity: X free, Y leased"
    capacity_pattern = r'current capacity:\s*(\d+)\s*free,\s*(\d+)\s*leased'
    match = re.search(capacity_pattern, log_content, re.IGNORECASE)

    if match:
        quota_details['free'] = int(match.group(1))
        quota_details['leased'] = int(match.group(2))
        quota_details['total'] = quota_details['free'] + quota_details['leased']

    return quota_details


def check_log_for_patterns(log_content: str, source_name: str = "unknown") -> List[Dict[str, str]]:
    """
    Check log content for known failure patterns.

    Args:
        log_content: Log file content as string
        source_name: Name of the log source (e.g., "build-log.txt", "extended.log")

    Returns:
        List of dictionaries with pattern name and source
    """
    detected_patterns = []

    for pattern_name, regexes in FAILURE_PATTERNS.items():
        for regex in regexes:
            if re.search(regex, log_content, re.IGNORECASE):
                detected_patterns.append({
                    'pattern': pattern_name,
                    'source': source_name
                })
                break  # Only add each pattern once

    return detected_patterns


def categorize_failing_tests(test_results: Dict) -> Dict[str, List[Dict]]:
    """
    Group failing tests by category.

    Args:
        test_results: Parsed test results

    Returns:
        Dictionary mapping category to list of failing tests
    """
    failing_tests = extract_failing_tests(test_results)
    categorized = defaultdict(list)

    for test in failing_tests:
        category = categorize_test_by_name(test['name'])
        categorized[category].append(test)

    return dict(categorized)


def categorize_test_list(failing_tests: List[Dict]) -> Dict[str, List[Dict]]:
    """
    Group a list of failing tests by category.

    Args:
        failing_tests: List of failing test dictionaries

    Returns:
        Dictionary mapping category to list of failing tests
    """
    categorized = defaultdict(list)

    for test in failing_tests:
        category = categorize_test_by_name(test['name'])
        categorized[category].append(test)

    return dict(categorized)


def _failed_test_case_ids(failing_tests: List[Dict]) -> List[str]:
    """Unique C##### identifiers from failing test entries, stable order."""
    seen: List[str] = []
    for t in failing_tests:
        cid = (t.get('test_case_number') or '').strip()
        if cid and cid not in seen:
            seen.append(cid)
    return seen


def _merge_case_ids(test_ids: List[str], log_ids: Optional[List[str]]) -> List[str]:
    """Append log-only IDs after test-results order, de-duplicated."""
    out: List[str] = list(test_ids)
    if not log_ids:
        return out
    for cid in log_ids:
        if cid not in out:
            out.append(cid)
    return out


def extract_failed_case_ids_from_extended_build_log(log_text: str) -> List[str]:
    """
    Find failed OSC test case IDs (C#####) in openshift-extended-test/build-log.txt.

    Used when test-results.yaml is missing or the step was interrupted; logs still
    often mention ``-C#####-`` on failure lines or in trailing summaries.
    """
    if not log_text:
        return []

    seen: List[str] = []
    id_in_name = re.compile(r'-C(\d+)-')
    # Lines that suggest a failure context (avoid counting passing test name mentions)
    failure_hint = re.compile(
        r'(?i)(fail|failure|failed|error|panic|timeout|interrupt|aborted|'
        r'SIGKILL|SIGTERM|OOMKilled|•\s*Failure|\[Fail\]|Summarizing\s+\d+\s+Failure)'
    )

    for line in log_text.splitlines():
        if not failure_hint.search(line):
            continue
        for m in id_in_name.finditer(line):
            cid = f"C{m.group(1)}"
            if cid not in seen:
                seen.append(cid)

    # Tail often has failure summaries after interruption or OOM
    if not seen:
        tail = log_text[-200000:] if len(log_text) > 200000 else log_text
        tail_fail = re.compile(r'(?i)(fail|failure|error|panic|timeout|interrupt|abort)')
        for line in tail.splitlines():
            if not tail_fail.search(line):
                continue
            for m in id_in_name.finditer(line):
                cid = f"C{m.group(1)}"
                if cid not in seen:
                    seen.append(cid)

    return seen


def determine_root_cause(
    failing_tests: List[Dict],
    detected_patterns: List[Dict],
    metadata: Dict,
    build_log_text: Optional[str] = None,
    extended_build_log_case_ids: Optional[List[str]] = None,
) -> Dict:
    """
    Attempt to determine root cause of failure.

    Args:
        failing_tests: List of failing tests
        detected_patterns: List of detected failure patterns with sources
        metadata: Job metadata
        build_log_text: Top-level Prow build log content for extracting additional details
        extended_build_log_case_ids: C##### IDs parsed from openshift-extended-test/build-log.txt

    Returns:
        Dictionary with root cause analysis
    """
    root_cause = {
        'primary_pattern': None,
        'likely_cause': '',
        'confidence': 'low',
        'suggested_actions': [],
    }

    # Extract just pattern names for easier checking
    pattern_names = [p['pattern'] for p in detected_patterns]

    # Function to find source of a specific pattern
    def find_pattern_source(pattern_name: str) -> Optional[str]:
        for pattern_info in detected_patterns:
            if pattern_info['pattern'] == pattern_name:
                return pattern_info['source']
        return None

    # Analyze patterns
    if 'timeout' in pattern_names:
        root_cause['primary_pattern'] = 'timeout'
        root_cause['pattern_source'] = find_pattern_source('timeout')
        root_cause['likely_cause'] = 'Test execution exceeded time limits'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Check if cluster resources are sufficient',
            'Review slow-running tests',
            'Consider increasing timeout if tests are valid',
        ]
    elif 'oom' in pattern_names:
        root_cause['primary_pattern'] = 'oom'
        root_cause['pattern_source'] = find_pattern_source('oom')
        root_cause['likely_cause'] = 'Out of memory condition'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Increase memory limits for pods',
            'Check for memory leaks in application',
            'Review cluster capacity',
        ]
    elif 'kata_init' in pattern_names:
        root_cause['primary_pattern'] = 'kata_init'
        root_cause['pattern_source'] = find_pattern_source('kata_init')
        root_cause['likely_cause'] = 'Kata runtime initialization failure'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Check Kata runtime logs',
            'Verify RPM compatibility',
            'Review node configuration',
        ]
    elif 'network' in pattern_names:
        root_cause['primary_pattern'] = 'network'
        root_cause['pattern_source'] = find_pattern_source('network')
        root_cause['likely_cause'] = 'Network connectivity issues'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Check network policies',
            'Verify DNS resolution',
            'Review firewall rules',
        ]
    elif 'image_pull' in pattern_names:
        root_cause['primary_pattern'] = 'image_pull'
        root_cause['pattern_source'] = find_pattern_source('image_pull')
        root_cause['likely_cause'] = 'Failed to pull container images'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Verify image exists and is accessible',
            'Check image pull secrets',
            'Review registry connectivity',
        ]
    elif 'azure_quota' in pattern_names:
        root_cause['primary_pattern'] = 'azure_quota'
        root_cause['pattern_source'] = find_pattern_source('azure_quota')
        root_cause['confidence'] = 'high'

        # Extract quota details if build log is available
        quota_details = None
        if build_log_text:
            quota_details = extract_azure_quota_details(build_log_text)

        if quota_details and quota_details['total'] is not None:
            root_cause['likely_cause'] = f"Azure quota slice exhausted - all {quota_details['total']} lease slots occupied ({quota_details['free']} free, {quota_details['leased']} leased)"
            root_cause['quota_details'] = quota_details
        else:
            root_cause['likely_cause'] = 'Azure quota slice exhausted - all lease slots occupied'

        root_cause['suggested_actions'] = [
            'Retry the job when Azure capacity becomes available',
            'No action needed - this is a transient infrastructure limitation',
            'Consider distributing test load across multiple time periods',
            'Monitor for recurring quota exhaustion patterns',
        ]
    elif 'quota' in pattern_names:
        root_cause['primary_pattern'] = 'quota'
        root_cause['pattern_source'] = find_pattern_source('quota')
        root_cause['likely_cause'] = 'Resource quota or limit range exceeded'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Check resource quotas and limit ranges',
            'Request quota increase if appropriate',
        ]
    elif 'infrastructure' in pattern_names:
        root_cause['primary_pattern'] = 'infrastructure'
        root_cause['pattern_source'] = find_pattern_source('infrastructure')
        root_cause['likely_cause'] = 'Cluster or node provisioning failure'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Review cluster health and provisioning logs',
            'Verify cluster installation completed',
        ]
    elif 'rpm_install' in pattern_names:
        root_cause['primary_pattern'] = 'rpm_install'
        root_cause['pattern_source'] = find_pattern_source('rpm_install')
        root_cause['likely_cause'] = 'RPM installation failed due to missing dependencies'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Check the first test logs for the specific missing dependency',
            'Verify the Kata RPM package dependencies are available in the repository',
            'Check if the RPM repository is accessible and configured correctly',
            'Verify the RPM version compatibility with the target OS',
        ]

        # If rpm_cascading is also present, note it's a secondary error
        if 'rpm_cascading' in pattern_names:
            root_cause['cascading_errors'] = ['rpm_cascading']
            root_cause['note'] = 'Multiple "Deployment is already in unlocked state" errors are cascading failures from the initial RPM dependency error'
    else:
        # Unknown pattern type: use first detected pattern
        if detected_patterns:
            primary = detected_patterns[0]
            pattern_name = primary.get('pattern', 'unknown') if isinstance(primary, dict) else 'unknown'
            root_cause['primary_pattern'] = pattern_name
            root_cause['pattern_source'] = primary.get('source') if isinstance(primary, dict) else None
            root_cause['likely_cause'] = f"Failure pattern: {pattern_name}"
            root_cause['confidence'] = 'medium'
            root_cause['suggested_actions'] = []

    # If many tests failed, might be infrastructure
    if len(failing_tests) > 10:
        if not root_cause['likely_cause']:  # Only set if no other cause was found
            root_cause['likely_cause'] = 'Widespread test failures suggest infrastructure or configuration issue'
            root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'].append('Review cluster health and OSC installation logs')
        root_cause['suggested_actions'].append('IMPORTANT: When most/all tests fail, examine the FIRST test\'s logs for the root cause - subsequent test errors are often cascading failures')

    # openshift-extended-test: failed test case numbers from test-results.yaml and/or step build-log.txt
    test_ids = _failed_test_case_ids(failing_tests)
    merged_ids = _merge_case_ids(test_ids, extended_build_log_case_ids)
    only_from_log: List[str] = []
    if extended_build_log_case_ids:
        ts = set(test_ids)
        only_from_log = [c for c in extended_build_log_case_ids if c not in ts]

    if merged_ids:
        root_cause['failed_test_case_numbers'] = merged_ids
        if only_from_log:
            root_cause['failed_test_case_numbers_from_build_log'] = only_from_log
        case_part = f"Failed test case(s): {', '.join(merged_ids)}."
        if only_from_log:
            case_part += " (includes IDs from openshift-extended-test/build-log.txt)."
    elif failing_tests:
        root_cause['failed_test_case_numbers'] = []
        case_part = (
            f"openshift-extended-test: {len(failing_tests)} failing test(s) "
            "(no C##### id in test names or build-log.txt)."
        )
    elif extended_build_log_case_ids is not None:
        root_cause['failed_test_case_numbers'] = []
        case_part = (
            "openshift-extended-test: step failed or interrupted; "
            "no C##### ids found in openshift-extended-test/build-log.txt (inspect artifact manually)."
        )
    else:
        return root_cause

    if root_cause.get('likely_cause'):
        root_cause['likely_cause'] = f"{root_cause['likely_cause'].rstrip()} {case_part}"
    else:
        root_cause['likely_cause'] = case_part

    return root_cause


def analyze_failure(
    prowjob_data: Dict,
    base_url: str,
    test_results: Optional[Dict],
    metadata: Dict
) -> Dict:
    """
    Comprehensive failure analysis.

    Args:
        prowjob_data: Parsed prowjob data
        base_url: Base URL for fetching artifacts
        test_results: Parsed test results
        metadata: Extracted metadata

    Returns:
        Dictionary with complete failure analysis
    """
    analysis = {
        'failure_location': 'unknown',
        'failing_tests': [],
        'failing_tests_by_category': {},
        'detected_patterns': [],
        'root_cause': {},
    }

    # Identify failure location (actual step name(s))
    variant = metadata.get('variant', '')
    analysis['failure_location'] = identify_failure_location(
        prowjob_data,
        test_results,
        base_url,
        variant
    )

    # Get failing tests if available
    if test_results:
        failing_tests = extract_failing_tests(test_results)
        analysis['failing_tests'] = failing_tests
        analysis['failing_tests_by_category'] = categorize_failing_tests(test_results)

    # Fetch and analyze logs for patterns
    detected_patterns = []
    build_log_text = None
    build_log_text_full = None  # Keep full log for execution order extraction
    analyzed_files = []
    extended_build_log_case_ids: Optional[List[str]] = None

    # First check the main build log for infrastructure-level failures (e.g., Azure quota)
    build_log_content = fetch_artifact(base_url, "build-log.txt")
    if build_log_content:
        try:
            build_log_text_full = build_log_content.decode('utf-8', errors='ignore')
            # Use truncated version for pattern matching (performance)
            build_log_text = build_log_text_full
            if len(build_log_text) > 100000:
                build_log_text = build_log_text[-100000:]
            patterns_from_build_log = check_log_for_patterns(build_log_text, "build-log.txt")
            detected_patterns.extend(patterns_from_build_log)
            analyzed_files.append({
                'name': 'build-log.txt',
                'path': 'build-log.txt',
                'patterns_found': len(patterns_from_build_log)
            })
            logger.debug(f"Patterns found in build-log.txt: {[p['pattern'] for p in patterns_from_build_log]}")
        except Exception as e:
            logger.warning(f"Failed to analyze build log: {e}")

    # Also check extended test log if available
    variant = metadata.get('variant', '')
    if variant:
        # Try to fetch extended test log
        extended_log_path = f"artifacts/{variant}/openshift-extended-test/artifacts/extended.log"
        log_content = fetch_artifact(base_url, extended_log_path)

        if log_content:
            try:
                log_text = log_content.decode('utf-8', errors='ignore')
                # Limit to last 100KB to avoid processing huge logs
                if len(log_text) > 100000:
                    log_text = log_text[-100000:]
                patterns_from_extended_log = check_log_for_patterns(log_text, "extended.log")
                detected_patterns.extend(patterns_from_extended_log)
                analyzed_files.append({
                    'name': 'extended.log',
                    'path': extended_log_path,
                    'patterns_found': len(patterns_from_extended_log)
                })
                logger.debug(f"Additional patterns found in extended.log: {[p['pattern'] for p in patterns_from_extended_log]}")
            except Exception as e:
                logger.warning(f"Failed to analyze extended log: {e}")

        # openshift-extended-test/build-log.txt — C##### when test-results is incomplete or step interrupted
        ext_step_build_path = f"artifacts/{variant}/openshift-extended-test/build-log.txt"
        ext_step_build = fetch_artifact(base_url, ext_step_build_path)
        if ext_step_build:
            try:
                ext_bl_text = ext_step_build.decode('utf-8', errors='ignore')
                extended_build_log_case_ids = extract_failed_case_ids_from_extended_build_log(ext_bl_text)
                analyzed_files.append({
                    'name': 'openshift-extended-test/build-log.txt',
                    'path': ext_step_build_path,
                    'patterns_found': len(extended_build_log_case_ids),
                })
                logger.debug(
                    "Case IDs from openshift-extended-test/build-log.txt: %s",
                    extended_build_log_case_ids,
                )
            except Exception as e:
                logger.warning(f"Failed to parse openshift-extended-test/build-log.txt: {e}")
                extended_build_log_case_ids = []

    # Store detected patterns with sources and analyzed files info
    analysis['detected_patterns'] = detected_patterns
    analysis['analyzed_files'] = analyzed_files

    # Add execution order to failing tests
    if analysis['failing_tests'] and build_log_text_full:
        analysis['failing_tests'] = add_execution_order_to_tests(
            analysis['failing_tests'],
            build_log_text_full
        )
        # Re-categorize with execution order included
        analysis['failing_tests_by_category'] = categorize_test_list(analysis['failing_tests'])

    # Determine root cause
    analysis['root_cause'] = determine_root_cause(
        analysis['failing_tests'],
        analysis['detected_patterns'],
        metadata,
        build_log_text,
        extended_build_log_case_ids,
    )

    return analysis
