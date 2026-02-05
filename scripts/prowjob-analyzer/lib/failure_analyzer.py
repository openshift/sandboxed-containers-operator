"""
Failure Analyzer Module

Analyzes failures to identify location, patterns, and root causes.
"""

import re
import logging
from typing import Dict, List, Optional
from collections import defaultdict
from .parser import categorize_test_by_name, extract_failing_tests
from .fetcher import fetch_artifact, get_failed_steps

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


def determine_root_cause(failing_tests: List[Dict], detected_patterns: List[Dict], metadata: Dict, build_log_text: Optional[str] = None) -> Dict:
    """
    Attempt to determine root cause of failure.

    Args:
        failing_tests: List of failing tests
        detected_patterns: List of detected failure patterns with sources
        metadata: Job metadata
        build_log_text: Build log content for extracting additional details

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

    # Check for version mismatch (OSC-specific)
    if any('version' in test['name'].lower() for test in failing_tests):
        root_cause['likely_cause'] = 'Operator version mismatch'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'].append(
            f"Update EXPECTED_OPERATOR_VERSION to {metadata.get('expected_operator_version', 'correct value')}"
        )

    # If many tests failed, might be infrastructure
    if len(failing_tests) > 10:
        if not root_cause['likely_cause']:  # Only set if no other cause was found
            root_cause['likely_cause'] = 'Widespread test failures suggest infrastructure or configuration issue'
            root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'].append('Review cluster health and OSC installation logs')
        root_cause['suggested_actions'].append('IMPORTANT: When most/all tests fail, examine the FIRST test\'s logs for the root cause - subsequent test errors are often cascading failures')

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
    analyzed_files = []

    # First check the main build log for infrastructure-level failures (e.g., Azure quota)
    build_log_content = fetch_artifact(base_url, "build-log.txt")
    if build_log_content:
        try:
            build_log_text = build_log_content.decode('utf-8', errors='ignore')
            # Limit to last 100KB to avoid processing huge logs
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

    # Store detected patterns with sources and analyzed files info
    analysis['detected_patterns'] = detected_patterns
    analysis['analyzed_files'] = analyzed_files

    # Determine root cause
    analysis['root_cause'] = determine_root_cause(
        analysis['failing_tests'],
        analysis['detected_patterns'],
        metadata,
        build_log_text
    )

    return analysis
