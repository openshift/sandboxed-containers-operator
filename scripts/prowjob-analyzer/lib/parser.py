"""
Parser Module

Handles parsing of prowjob.json and test-results.yaml files.
"""

import logging
from typing import Dict, Optional, List
from datetime import datetime

try:
    import yaml
except ImportError:
    # Fallback if pyyaml not available
    yaml = None

logger = logging.getLogger(__name__)


def parse_prowjob(prowjob_data: Dict) -> Dict:
    """
    Parse prowjob.json and extract key information.

    Args:
        prowjob_data: Parsed prowjob.json content

    Returns:
        Dictionary with extracted prowjob information
    """
    spec = prowjob_data.get('spec', {})
    status = prowjob_data.get('status', {})
    metadata = prowjob_data.get('metadata', {})

    job_info = {
        'job_name': spec.get('job', 'unknown'),
        'type': spec.get('type', 'unknown'),  # periodic, presubmit, postsubmit
        'cluster': spec.get('cluster', 'unknown'),
        'build_id': status.get('build_id', metadata.get('name', 'unknown')),
        'state': status.get('state', 'unknown'),  # success, failure, pending, etc.
        'url': status.get('url', ''),
        'start_time': status.get('startTime', ''),
        'completion_time': status.get('completionTime', ''),
        'duration_seconds': 0,
        'pod_name': status.get('pod_name', ''),
        'metadata': metadata,  # Include full metadata (contains labels and annotations)
    }

    # Calculate duration
    if job_info['start_time'] and job_info['completion_time']:
        try:
            start = datetime.fromisoformat(job_info['start_time'].replace('Z', '+00:00'))
            end = datetime.fromisoformat(job_info['completion_time'].replace('Z', '+00:00'))
            job_info['duration_seconds'] = int((end - start).total_seconds())
        except Exception as e:
            logger.warning(f"Failed to calculate duration: {e}")

    # Extract environment variables (useful for metadata extraction)
    env_vars = {}
    pod_spec = spec.get('pod_spec', {})
    containers = pod_spec.get('containers', [])
    if containers:
        for env_var in containers[0].get('env', []):
            env_vars[env_var.get('name', '')] = env_var.get('value', '')

    job_info['env_vars'] = env_vars

    return job_info


def parse_test_results(test_results_content: bytes) -> Optional[Dict]:
    """
    Parse test-results.yaml file.

    Args:
        test_results_content: Raw bytes content of test-results.yaml

    Returns:
        Parsed test results as dict, or None if parsing fails
    """
    if yaml is None:
        logger.error("pyyaml not available, cannot parse test-results.yaml")
        return None

    if not test_results_content:
        return None

    try:
        content_str = test_results_content.decode('utf-8')
        data = yaml.safe_load(content_str)

        # Handle nested structure - test-results.yaml often has top-level key
        # like "openshift-extended-test" containing the actual data
        if data and isinstance(data, dict):
            # Check if there's a single top-level key with the actual test data
            if len(data) == 1:
                first_key = list(data.keys())[0]
                first_value = data[first_key]
                if isinstance(first_value, dict) and 'total' in first_value:
                    # This is the nested structure, unwrap it
                    return first_value

        return data
    except (yaml.YAMLError, UnicodeDecodeError) as e:
        logger.error(f"Failed to parse test-results.yaml: {e}")
        return None


def get_job_status(prowjob_data: Dict, test_results: Optional[Dict] = None) -> str:
    """
    Determine overall job status.

    Args:
        prowjob_data: Parsed prowjob.json
        test_results: Optional parsed test-results.yaml

    Returns:
        Status string: 'success', 'failure', 'timeout', 'error', 'aborted', 'pending'
    """
    status = prowjob_data.get('status', {})
    state = status.get('state', '').lower()

    # Map Prow states to our status
    if state == 'success':
        # Double-check with test results if available
        if test_results and test_results.get('failures', 0) > 0:
            return 'failure'
        return 'success'
    elif state == 'failure':
        return 'failure'
    elif state == 'aborted':
        return 'aborted'
    elif state == 'pending':
        return 'pending'
    elif state == 'error':
        return 'error'
    else:
        # Try to infer from test results
        if test_results:
            if test_results.get('failures', 0) == 0:
                return 'success'
            else:
                return 'failure'
        return 'unknown'


def parse_test_case_info(test_name: str) -> Dict:
    """
    Parse test case number and description from test name.

    Test names typically follow patterns like:
    "[sig-kata] Kata Author:user-Priority-C12345-test description [Serial]"

    Args:
        test_name: Full test name

    Returns:
        Dictionary with test_case_number and description
    """
    import re

    info = {
        'test_case_number': '',
        'description': '',
        'priority': '',
        'author': '',
    }

    # Extract test case number (C##### pattern)
    case_match = re.search(r'-C(\d+)-', test_name)
    if case_match:
        info['test_case_number'] = f"C{case_match.group(1)}"

    # Extract priority (High, Medium, Low)
    priority_match = re.search(r'-(High|Medium|Low)-', test_name)
    if priority_match:
        info['priority'] = priority_match.group(1)

    # Extract author
    author_match = re.search(r'Author:([^-]+)-', test_name)
    if author_match:
        info['author'] = author_match.group(1)

    # Extract description (after C##### until [Serial] or end)
    # Pattern: -C#####-description [optional tags]
    desc_match = re.search(r'-C\d+-(.*?)(?:\s*\[|$)', test_name)
    if desc_match:
        info['description'] = desc_match.group(1).strip()
    else:
        # Fallback: try to extract anything after the last - until [
        desc_match2 = re.search(r'-([^-\[]+)(?:\s*\[|$)', test_name)
        if desc_match2:
            info['description'] = desc_match2.group(1).strip()

    # If no description found, use full test name
    if not info['description']:
        info['description'] = test_name

    return info


def extract_failing_tests(test_results: Dict) -> List[Dict]:
    """
    Extract list of failing tests from test-results.yaml.

    Args:
        test_results: Parsed test-results.yaml

    Returns:
        List of failing test dictionaries with name and details
    """
    failing_tests = []

    if not test_results:
        return failing_tests

    # Check for 'failingScenarios' list (simpler format)
    failing_scenarios = test_results.get('failingScenarios', [])
    if failing_scenarios:
        for test_name in failing_scenarios:
            case_info = parse_test_case_info(test_name)
            failing_tests.append({
                'name': test_name,
                'duration': 0,
                'failure_message': '',
                'system_out': '',
                'system_err': '',
                'test_case_number': case_info['test_case_number'],
                'description': case_info['description'],
                'priority': case_info['priority'],
                'author': case_info['author'],
            })
        return failing_tests

    # Fall back to 'tests' list (detailed format)
    tests = test_results.get('tests', [])

    for test in tests:
        # Check if test failed
        if test.get('state') == 'failed' or test.get('failed', False):
            test_name = test.get('name', 'unknown')
            case_info = parse_test_case_info(test_name)

            failing_tests.append({
                'name': test_name,
                'duration': test.get('duration', 0),
                'failure_message': test.get('failureMessage', ''),
                'system_out': test.get('systemOut', ''),
                'system_err': test.get('systemErr', ''),
                'test_case_number': case_info['test_case_number'],
                'description': case_info['description'],
                'priority': case_info['priority'],
                'author': case_info['author'],
            })

    return failing_tests


def categorize_test_by_name(test_name: str) -> str:
    """
    Categorize a test based on its name.

    Args:
        test_name: Full test name

    Returns:
        Category string (e.g., 'deployment', 'networking', 'storage', etc.)
    """
    test_lower = test_name.lower()

    # OSC/Kata specific categorization
    if 'deploy' in test_lower or 'create' in test_lower or 'delete' in test_lower:
        return 'deployment'
    elif 'network' in test_lower or 'service' in test_lower or 'expose' in test_lower:
        return 'networking'
    elif 'storage' in test_lower or 'volume' in test_lower or 'pv' in test_lower:
        return 'storage'
    elif 'cpu' in test_lower or 'memory' in test_lower or 'resource' in test_lower or 'overhead' in test_lower:
        return 'resources'
    elif 'log' in test_lower or 'cli' in test_lower:
        return 'cli_logging'
    elif 'scale' in test_lower:
        return 'scaling'
    elif 'image' in test_lower or 'annotation' in test_lower:
        return 'images_annotations'
    elif 'version' in test_lower or 'csv' in test_lower:
        return 'versioning'
    elif 'operator' in test_lower:
        return 'operator'
    elif 'peerpod' in test_lower or 'peer-pod' in test_lower:
        return 'peerpods'
    elif 'kata' in test_lower:
        return 'kata_runtime'
    else:
        return 'other'


def format_duration(seconds: int) -> str:
    """
    Format duration in seconds to human-readable format.

    Args:
        seconds: Duration in seconds

    Returns:
        Formatted string (e.g., "1h 23m 45s")
    """
    if seconds < 60:
        return f"{seconds}s"
    elif seconds < 3600:
        minutes = seconds // 60
        secs = seconds % 60
        return f"{minutes}m {secs}s"
    else:
        hours = seconds // 3600
        minutes = (seconds % 3600) // 60
        secs = seconds % 60
        return f"{hours}h {minutes}m {secs}s"
