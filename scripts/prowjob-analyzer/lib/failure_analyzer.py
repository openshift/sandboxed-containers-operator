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
    'kata_init': [
        r'kata.*failed to start',
        r'runtime kata.*error',
        r'qemu.*failed',
        r'failed to create sandbox',
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




def check_log_for_patterns(log_content: str) -> List[str]:
    """
    Check log content for known failure patterns.

    Args:
        log_content: Log file content as string

    Returns:
        List of detected pattern names
    """
    detected_patterns = []

    for pattern_name, regexes in FAILURE_PATTERNS.items():
        for regex in regexes:
            if re.search(regex, log_content, re.IGNORECASE):
                detected_patterns.append(pattern_name)
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


def determine_root_cause(failing_tests: List[Dict], detected_patterns: List[str], metadata: Dict) -> Dict:
    """
    Attempt to determine root cause of failure.

    Args:
        failing_tests: List of failing tests
        detected_patterns: List of detected failure patterns
        metadata: Job metadata

    Returns:
        Dictionary with root cause analysis
    """
    root_cause = {
        'primary_pattern': None,
        'likely_cause': '',
        'confidence': 'low',
        'suggested_actions': [],
    }

    # Analyze patterns
    if 'timeout' in detected_patterns:
        root_cause['primary_pattern'] = 'timeout'
        root_cause['likely_cause'] = 'Test execution exceeded time limits'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Check if cluster resources are sufficient',
            'Review slow-running tests',
            'Consider increasing timeout if tests are valid',
        ]
    elif 'oom' in detected_patterns:
        root_cause['primary_pattern'] = 'oom'
        root_cause['likely_cause'] = 'Out of memory condition'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Increase memory limits for pods',
            'Check for memory leaks in application',
            'Review cluster capacity',
        ]
    elif 'kata_init' in detected_patterns:
        root_cause['primary_pattern'] = 'kata_init'
        root_cause['likely_cause'] = 'Kata runtime initialization failure'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Check Kata runtime logs',
            'Verify RPM compatibility',
            'Review node configuration',
        ]
    elif 'network' in detected_patterns:
        root_cause['primary_pattern'] = 'network'
        root_cause['likely_cause'] = 'Network connectivity issues'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'] = [
            'Check network policies',
            'Verify DNS resolution',
            'Review firewall rules',
        ]
    elif 'image_pull' in detected_patterns:
        root_cause['primary_pattern'] = 'image_pull'
        root_cause['likely_cause'] = 'Failed to pull container images'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'] = [
            'Verify image exists and is accessible',
            'Check image pull secrets',
            'Review registry connectivity',
        ]

    # Check for version mismatch (OSC-specific)
    if any('version' in test['name'].lower() for test in failing_tests):
        root_cause['likely_cause'] = 'Operator version mismatch'
        root_cause['confidence'] = 'high'
        root_cause['suggested_actions'].append(
            f"Update EXPECTED_OPERATOR_VERSION to {metadata.get('expected_operator_version', 'correct value')}"
        )

    # If many tests failed, might be infrastructure
    if len(failing_tests) > 10:
        root_cause['likely_cause'] = 'Widespread test failures suggest infrastructure or configuration issue'
        root_cause['confidence'] = 'medium'
        root_cause['suggested_actions'].append('Review cluster health and OSC installation logs')

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
                analysis['detected_patterns'] = check_log_for_patterns(log_text)
            except Exception as e:
                logger.warning(f"Failed to analyze log: {e}")

    # Determine root cause
    analysis['root_cause'] = determine_root_cause(
        analysis['failing_tests'],
        analysis['detected_patterns'],
        metadata
    )

    return analysis
