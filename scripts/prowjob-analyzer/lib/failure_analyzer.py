"""
Failure Analyzer Module

Analyzes failures to identify location, patterns, and root causes.
"""

import re
import logging
from typing import Dict, List, Optional
from collections import defaultdict
from .parser import (
    categorize_test_by_name,
    extract_failing_testcases_from_junit,
    parse_junit_xml,
    parse_osc_suite_test_case_info,
)
from .fetcher import (
    ALL_TESTSUITES_STEPS,
    TESTSUITES_GATE_STEP,
    TESTSUITES_SUITE_STEPS,
    _podutils_json_timestamp,
    compute_step_elapsed_display,
    fetch_artifact,
    fetch_json_artifact,
    get_failed_steps,
    get_step_directories,
    list_artifact_files,
)

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
        # oc debug on worker + kata RPM (BeforeEach); see detect_kata_rpm_install_context too
        r'debug\s+node/[^\s]*worker[^\s]*.*kata-containers\.rpm',
        r'is needed by kata-containers',
        r'error:.*Failed dependencies',
        r'error:.*package.*is needed by kata',
        r'rpm.*installation failed',
        r'error:.*nothing provides.*needed by kata',
    ],
    'rpm_cascading': [
        r'error:.*Deployment is already in unlocked state',
        r'error:.*ostree.*unlocked state',
    ],
    # Migrated from the osc-step-debug skill's documented "Common Error
    # Patterns" (~/go/src/github.com/tbuskey/skills-marketplace,
    # plugins/skills/osc-step-debug/skills/osc-step-debug/SKILL.md) -- that
    # skill's own analysis is supposed to be a thin wrapper around this tool
    # (see analyze-prowjob.sh), so these patterns belong here, not
    # re-implemented in its bash. Relevant mostly to install-osc-operator/
    # install-trustee-operator (pre-phase) failures, which matter more now
    # that the test: phase is a thin gate (see the prowjob-analyzer plan).
    'catalogsource': [
        r'CatalogSource.*not.*READY',
        r'CatalogSource.*not\s+found',
        r'failed to get catalogsource',
    ],
    'olm_subscription': [
        r'Subscription.*has no InstallPlan',
        r'ConstraintsNotSatisfiable',
        r'no operators found (in|matching)',
        r'InstallPlan.*failed',
    ],
    'kbs_auth': [
        # Trustee/KBS attestation failures (confidential-containers workloads).
        # Version note: trustee 1.1.x pairs with kbs-client v0.17.0, trustee
        # 1.11.x with kbs-client v0.19.0 -- a mismatch here is a common cause
        # of auth failures that look identical to a real KBS outage.
        r'kbs-client.*Authenticating.*failed',
        r'404.*(?:kbs|resource).*not\s+found',
        r'KbsConfig.*not\s+found',
        r'attestation.*failed',
    ],
}


def identify_failure_location(
    prowjob_data: Dict,
    base_url: str,
    variant: str,
) -> str:
    """
    Identify which step(s) failed in the job, for the variant-unknown fallback path
    (see :func:`classify_testsuites_result` for the normal, variant-known path).

    Args:
        prowjob_data: Parsed prowjob data
        base_url: Base URL of Prow job
        variant: Job variant

    Returns:
        Name of failed step(s), comma-separated if multiple, or status like 'timeout'/'unknown'
    """
    state = prowjob_data.get('state', '').lower()

    if 'timeout' in state or state == 'aborted':
        return 'timeout'

    if state in ['error', 'errored']:
        return 'infrastructure'

    if variant and variant != 'unknown':
        failed_steps = get_failed_steps(base_url, variant)
        if failed_steps:
            return ', '.join(failed_steps)
        if state == 'failure':
            logger.warning("Job failed but couldn't detect failed steps from artifacts")
            return 'unknown'

    return 'unknown'


def _step_result(finished_json: Optional[Dict]) -> Optional[str]:
    """'SUCCESS'/'FAILURE'/None (missing or inconclusive) from a step's finished.json."""
    if not isinstance(finished_json, dict):
        return None
    result = (finished_json.get('result') or '').upper()
    if result:
        return result
    passed = finished_json.get('passed')
    if passed is True:
        return 'SUCCESS'
    if passed is False:
        return 'FAILURE'
    return None


def classify_testsuites_result(base_url: str, variant: str) -> Dict:
    """
    Classify the sandboxed-containers-operator-testsuites chain's outcome into the
    three cases the prowjob-analyzer plan documents:

    1. **Blocking phase failure** -- the test:-phase gate step never ran (pre
       failed, so ci-operator skipped the whole test: phase) or the gate step
       itself failed. Root cause is upstream of testing entirely.
    2. **Best-effort suite failure** -- gate succeeded, but one or more of the
       independent testsuites-{osc,kata-upstream,caa} steps failed on its own
       finished.json. The job is still FAILURE overall even though the other
       suites/post steps ran fine (best_effort: true only means a failing
       suite doesn't block its siblings).
    3. **Unrelated post-step failure** -- the whole testsuites chain looks
       clean, but some other step (gather-must-gather, deprovision,
       cucushift-installer-wait, ...) failed independently.

    Returns:
        {
          'gate_step_exists': bool, 'gate_status': 'SUCCESS'|'FAILURE'|None,
          'suites': {suite_key: {'step_name', 'step_exists', 'status'}, ...},
          'other_failed_steps': [...],  # failed steps outside the testsuites chain
          'latest_finished_json': dict or None,  # furthest-progressed step's finished.json
          'failure_kind': 'blocking_phase_failure' | 'best_effort_suite_failure' |
                           'unrelated_post_step_failure' | None,
        }
    """
    step_dirs = set(get_step_directories(base_url, variant))

    result: Dict = {
        'gate_step_exists': TESTSUITES_GATE_STEP in step_dirs,
        'gate_status': None,
        'suites': {},
        'other_failed_steps': [],
        'latest_finished_json': None,
    }

    latest_ts = -1

    def _consider_latest(finished_json: Optional[Dict]) -> None:
        nonlocal latest_ts
        ts = _podutils_json_timestamp(finished_json)
        if ts is not None and ts > latest_ts:
            latest_ts = ts
            result['latest_finished_json'] = finished_json

    if result['gate_step_exists']:
        gate_finished = fetch_json_artifact(
            base_url, f"artifacts/{variant}/{TESTSUITES_GATE_STEP}/finished.json"
        )
        result['gate_status'] = _step_result(gate_finished)
        _consider_latest(gate_finished)

    for suite_key, step_name in TESTSUITES_SUITE_STEPS.items():
        exists = step_name in step_dirs
        status = None
        if exists:
            finished = fetch_json_artifact(base_url, f"artifacts/{variant}/{step_name}/finished.json")
            status = _step_result(finished)
            _consider_latest(finished)
        result['suites'][suite_key] = {
            'step_name': step_name,
            'step_exists': exists,
            'status': status,
        }

    known_steps = set(ALL_TESTSUITES_STEPS)
    result['other_failed_steps'] = [
        s for s in get_failed_steps(base_url, variant) if s not in known_steps
    ]

    if not result['gate_step_exists'] or result['gate_status'] == 'FAILURE':
        result['failure_kind'] = 'blocking_phase_failure'
    elif any(s['status'] == 'FAILURE' for s in result['suites'].values()):
        result['failure_kind'] = 'best_effort_suite_failure'
    elif result['other_failed_steps']:
        result['failure_kind'] = 'unrelated_post_step_failure'
    else:
        result['failure_kind'] = None

    return result


def _junit_totals(reports: List[Dict]) -> Dict[str, int]:
    """Sum tests/failures/errors/skipped across every JUnit report for one suite."""
    totals = {'tests': 0, 'failures': 0, 'errors': 0, 'skipped': 0}
    for report in reports:
        j = report['junit']
        for key in totals:
            totals[key] += j.get(key, 0)
    totals['passed'] = max(0, totals['tests'] - totals['failures'] - totals['errors'] - totals['skipped'])
    return totals


def _sum_junit_elapsed_display(reports: List[Dict]) -> Optional[str]:
    """
    Sum each JUnit report's own top-level ``time`` (seconds) across every file
    for a suite (e.g. kata-upstream writes one file per bats suite, run
    sequentially), formatted as ``HH:MM``. ``None`` if there's no JUnit at all
    or none report a positive time.
    """
    if not reports:
        return None
    total_secs = sum((r['junit'].get('time') or 0.0) for r in reports)
    if total_secs <= 0:
        return None
    from .parser import format_hours_minutes_test_step_display
    return format_hours_minutes_test_step_display(int(round(total_secs)))


def summarize_gate(
    base_url: str,
    variant: str,
    step_exists: bool,
    status: Optional[str],
) -> Dict:
    """
    Summary for the test:-phase gate step: unlike the three post-phase
    suites, it writes no JUnit (just a touch marker), so this is status +
    elapsed time only. Included alongside the suites so a reader can see
    *every* testsuites-chain step's own pass/fail and duration, not just the
    aggregate `test_elapsed_time` for the whole chain.
    """
    base: Dict = {
        'suite': 'gate',
        'step_name': TESTSUITES_GATE_STEP,
        'step_exists': step_exists,
        'status': status,
        'elapsed_time': '',
    }
    if step_exists:
        base['elapsed_time'] = compute_step_elapsed_display(base_url, variant, TESTSUITES_GATE_STEP)
    return base


def summarize_suite(
    base_url: str,
    variant: str,
    suite_key: str,
    step_name: str,
    step_exists: bool,
    status: Optional[str],
) -> Dict:
    """
    Per-suite summary for reporting: elapsed time, pass/fail status, and
    JUnit-derived test counts for every suite (whether it passed, failed, or
    self-skipped), plus real failing-test detail when it failed. Used to
    show each of osc/kata-upstream/caa independently (see the
    prowjob-analyzer plan, Workstream E) instead of only surfacing detail
    for whichever suite happened to fail.
    """
    base: Dict = {
        'suite': suite_key,
        'step_name': step_name,
        'step_exists': step_exists,
        'status': status,
        'elapsed_time': '',
        'tests': 0, 'passed': 0, 'failures': 0, 'errors': 0, 'skipped': 0,
        'had_junit': False,
        'failing_tests': [],
        'detected_patterns': [],
    }

    if not step_exists:
        return base

    reports = _collect_suite_junit_reports(base_url, variant, suite_key, step_name)
    base['had_junit'] = bool(reports)
    base.update(_junit_totals(reports))

    # None of these steps write started.json (confirmed live), so
    # compute_step_elapsed_display's wall-clock path never applies here and
    # it falls back to scraping build-log.txt for a duration-shaped string
    # -- which only works for osc (openshift-tests/Ginkgo-style output);
    # kata-upstream (bats) and caa don't match those patterns. JUnit's own
    # <testsuites>/<testsuite> time= attribute is a more reliable source
    # when any JUnit exists at all, so prefer it first.
    base['elapsed_time'] = _sum_junit_elapsed_display(reports) or compute_step_elapsed_display(
        base_url, variant, step_name
    )

    if status == 'FAILURE':
        suite_result = analyze_suite_failure(base_url, variant, suite_key, step_name)
        base['failing_tests'] = suite_result['failing_tests']
        base['detected_patterns'] = suite_result['detected_patterns']
        base['no_results'] = suite_result['no_results']
        base['synthetic_no_results'] = suite_result['synthetic_no_results']

    return base


def summarize_all_suites(base_url: str, variant: str, classification: Dict) -> Dict[str, Dict]:
    """
    Per-step summaries (see :func:`summarize_gate`/:func:`summarize_suite`) for
    the test:-phase gate plus every post:-phase suite in the testsuites chain --
    each with its own elapsed time and status, in addition to (not instead of)
    the chain-wide aggregate `test_elapsed_time`/`failure_kind` summary.
    """
    summaries = {
        'gate': summarize_gate(
            base_url, variant,
            classification.get('gate_step_exists', False),
            classification.get('gate_status'),
        ),
    }
    for suite_key, info in classification.get('suites', {}).items():
        summaries[suite_key] = summarize_suite(
            base_url, variant, suite_key, info['step_name'], info['step_exists'], info['status']
        )
    return summaries


def _collect_suite_junit_reports(
    base_url: str, variant: str, suite_key: str, step_name: str
) -> List[Dict]:
    """Fetch+parse every JUnit XML artifact a testsuites-chain suite wrote for this run."""
    artifacts_dir = f"artifacts/{variant}/{step_name}/artifacts"
    name_prefix = {
        'osc': 'junit_osc',
        'kata-upstream': 'junit_kata_upstream_',
        'caa': 'junit_caa',
    }.get(suite_key, 'junit_')

    filenames = list_artifact_files(base_url, artifacts_dir, name_prefix=name_prefix)

    reports = []
    for name in filenames:
        content = fetch_artifact(base_url, f"{artifacts_dir}/{name}")
        if not content:
            continue
        parsed = parse_junit_xml(content)
        if parsed:
            reports.append({'file': name, 'junit': parsed})
    return reports


def analyze_suite_failure(base_url: str, variant: str, suite_key: str, step_name: str) -> Dict:
    """
    Detailed analysis for one failed testsuites-chain suite.

    Uses real JUnit-derived failing tests when the suite wrote any JUnit XML,
    falling back to the suite's own build-log.txt pattern scan when it didn't
    (the plan's documented osc/kata-upstream "exited non-zero, no JUnit at
    all" asymmetry vs. caa's synthetic junit_caa_noresults.xml).
    """
    reports = _collect_suite_junit_reports(base_url, variant, suite_key, step_name)
    had_junit = bool(reports)
    synthetic_no_results = any(r['file'] == 'junit_caa_noresults.xml' for r in reports)

    failing_tests: List[Dict] = []
    for report in reports:
        for tc in extract_failing_testcases_from_junit(report['junit']):
            if suite_key == 'osc':
                case_info = parse_osc_suite_test_case_info(tc['name'])
            else:
                case_info = {'test_case_number': '', 'description': tc['name']}
            failing_tests.append({
                'name': tc['name'],
                'suite': suite_key,
                'junit_file': report['file'],
                'duration': tc.get('time', 0),
                'failure_message': tc.get('failure_message', ''),
                'system_out': '',
                'system_err': tc.get('failure_text', ''),
                'test_case_number': case_info.get('test_case_number', ''),
                'description': case_info.get('description') or tc['name'],
                'priority': '',
                'author': '',
            })

    build_log_path = f"artifacts/{variant}/{step_name}/build-log.txt"
    build_log_content = fetch_artifact(base_url, build_log_path)
    detected_patterns: List[Dict] = []
    if build_log_content:
        build_log_text = build_log_content.decode('utf-8', errors='ignore')
        detected_patterns = check_log_for_patterns(build_log_text, f"{step_name}/build-log.txt")

    # The step failed, wrote no JUnit at all, and nothing recognizable in its
    # log either: a bare "suite errored, no per-test detail available" case
    # (distinct from caa's synthetic no-results JUnit, which at least shows up
    # as a real failing testcase above).
    no_results = (not had_junit) and not detected_patterns

    return {
        'suite': suite_key,
        'step_name': step_name,
        'failing_tests': failing_tests,
        'detected_patterns': detected_patterns,
        'had_junit': had_junit,
        'synthetic_no_results': synthetic_no_results,
        'no_results': no_results,
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


def extended_build_log_first_test_prefix(log_text: str, max_chars: int = 1_500_000) -> str:
    """
    Text through the end of the first ginkgo spec block. RPM install in BeforeEach and the
    first failure are before/at the second ``started: (i/total/n)`` line; later specs repeat
    the same install failure and add noise.
    """
    if not log_text:
        return ''
    text = log_text if len(log_text) <= max_chars else log_text[:max_chars]
    # Second ``started: (a/b/c)`` begins the next spec (Ginkgo/openshift-tests).
    pat = re.compile(r'^\s*started:\s*\(\d+/\d+/\d+\)', re.MULTILINE)
    matches = list(pat.finditer(text))
    if len(matches) >= 2:
        return text[: matches[1].start()]
    return text


def detect_kata_rpm_install_context(text: str) -> bool:
    """
    True when ``oc debug node/…worker…`` failed while installing kata-containers.rpm
    (Failed dependencies / needed by kata-containers).
    """
    if not text or len(text) < 80:
        return False
    if not re.search(r'debug\s+node/[^\s]*worker', text, re.I):
        return False
    if not re.search(r'kata-containers\.rpm|rpm\s+-Uvh\s+\S*kata', text, re.I):
        return False
    if not re.search(r'Failed dependencies|is needed by kata-containers', text, re.I):
        return False
    return True


def extract_kata_rpm_dependency_detail(text: str) -> Optional[str]:
    """First dependency line mentioning kata-containers, or Failed dependencies header."""
    if not text:
        return None
    for line in text.splitlines():
        s = line.strip()
        if re.search(r'is needed by kata-containers', s, re.I):
            return s[:500]
    m = re.search(r'error:\s*Failed dependencies', text, re.I)
    if m:
        rest = text[m.start() :]
        return rest.split('\n', 1)[0].strip()[:500]
    return None


def _rpm_install_is_verified_kata_worker(
    detected_patterns: List[Dict],
    oet_build_log_first_test: Optional[str],
) -> bool:
    """
    True only when ``rpm_install`` is tied to oc debug on a worker + kata RPM context.

    Generic ``FAILURE_PATTERNS['rpm_install']`` regexes can match unrelated RPM errors in
    top-level build-log.txt or extended.log; avoid high-confidence Kata remediation until
    verified via openshift-extended-test (or composite injection from
    :func:`detect_kata_rpm_install_context`).
    """
    for p in detected_patterns:
        if p.get('pattern') != 'rpm_install':
            continue
        src = (p.get('source') or '')
        # analyze_failure appends this only when detect_kata_rpm_install_context(oet prefix)
        if 'composite' in src:
            return True

    oet = oet_build_log_first_test or ''
    if not oet.strip():
        return False

    if detect_kata_rpm_install_context(oet):
        return True

    detail = extract_kata_rpm_dependency_detail(oet)
    if not detail:
        return False
    if not re.search(r'debug\s+node/[^\s]*worker', oet, re.I):
        return False
    if not re.search(r'kata-containers\.rpm|rpm\s+-Uvh\s+\S*kata|needed by kata-containers', oet, re.I):
        return False
    return True


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
    oet_build_log_first_test: Optional[str] = None,
) -> Dict:
    """
    Attempt to determine root cause of failure.

    Args:
        failing_tests: List of failing tests
        detected_patterns: List of detected failure patterns with sources
        metadata: Job metadata
        build_log_text: Top-level Prow build log content for extracting additional details
        extended_build_log_case_ids: C##### IDs parsed from openshift-extended-test/build-log.txt
        oet_build_log_first_test: openshift-extended-test/build-log.txt truncated to the first spec
            (used for kata RPM dependency extraction; avoids cascading repeats)

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

    # Analyze patterns (rpm_install before timeout: logs often contain both Prow/step timeouts
    # and the real Kata RPM failure on workers)
    if 'rpm_install' in pattern_names:
        root_cause['primary_pattern'] = 'rpm_install'
        root_cause['pattern_source'] = find_pattern_source('rpm_install')
        kata_worker_verified = _rpm_install_is_verified_kata_worker(
            detected_patterns, oet_build_log_first_test
        )
        root_cause['kata_worker_context_verified'] = kata_worker_verified

        if kata_worker_verified:
            root_cause['confidence'] = 'high'
            detail = None
            if oet_build_log_first_test:
                detail = extract_kata_rpm_dependency_detail(oet_build_log_first_test)
            if detail:
                root_cause['likely_cause'] = (
                    f'Kata RPM install on a worker (oc debug) failed: missing dependency. {detail}'
                )
            else:
                root_cause['likely_cause'] = (
                    'Kata RPM install on a worker node failed (BeforeEach): missing or unsatisfied '
                    'dependencies for kata-containers'
                )
            root_cause['suggested_actions'] = [
                'Use the first spec block in the relevant step\'s build-log.txt only; later specs '
                'repeat the same RPM install failure',
                'Resolve the dependency shown under StdOut from oc debug (e.g. qemu-kvm-core)',
                'Verify RHCOS layer and kata-containers RPM versions match test expectations',
            ]
            if 'rpm_cascading' in pattern_names:
                root_cause['cascading_errors'] = ['rpm_cascading']
                root_cause['note'] = (
                    'Additional ostree "already unlocked" errors are usually cascading from the initial RPM failure'
                )
        else:
            root_cause['confidence'] = 'low'
            root_cause['likely_cause'] = (
                'RPM installation or dependency error matched generic log patterns; not confirmed '
                'as Kata worker install via oc debug. Inspect the log in pattern_source for the '
                'exact rpm/dnf error before applying Kata-specific fixes.'
            )
            root_cause['suggested_actions'] = [
                'Open the file listed in pattern_source and locate the failing rpm/dnf line',
                'If the failure is a worker RPM install, confirm oc debug node/…worker… + '
                'kata-containers in that step\'s own build-log.txt',
            ]
            if 'rpm_cascading' in pattern_names:
                root_cause['cascading_errors'] = ['rpm_cascading']
                root_cause['note'] = (
                    'Additional ostree "already unlocked" errors may be related to an earlier RPM/ostree '
                    'issue in the same log; verify context before treating as Kata-specific cascade'
                )
    elif 'timeout' in pattern_names:
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
            case_part += " (includes IDs found via build-log.txt)."
    elif failing_tests:
        root_cause['failed_test_case_numbers'] = []
        case_part = (
            f"{len(failing_tests)} failing test(s) (no C##### id in test names or build-log.txt)."
        )
    elif extended_build_log_case_ids is not None:
        root_cause['failed_test_case_numbers'] = []
        case_part = (
            "Step failed or interrupted; no C##### ids found in its build-log.txt "
            "(inspect artifact manually)."
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
    metadata: Dict,
    classification: Optional[Dict] = None,
) -> Dict:
    """
    Comprehensive failure analysis for the sandboxed-containers-operator-testsuites
    chain architecture (see the prowjob-analyzer plan, Workstream C).

    Classifies the failure via :func:`classify_testsuites_result` (blocking
    phase failure vs. best-effort suite failure vs. unrelated post-step
    failure), pulls real per-test detail from JUnit XML for any failed suite
    (:func:`analyze_suite_failure`), and still scans the top-level
    build-log.txt for infra-level patterns (timeouts, Azure quota, etc.)
    regardless of which case applies.

    Args:
        prowjob_data: Parsed prowjob data
        base_url: Base URL for fetching artifacts
        metadata: Extracted metadata
        classification: Precomputed :func:`classify_testsuites_result` output,
            when the caller already has one (e.g. dig.py computes it once for
            both the status check and the per-suite report section). Computed
            internally when omitted; artifact fetches are cached either way,
            so passing it in only saves the step-directory listing call.

    Returns:
        Dictionary with complete failure analysis
    """
    analysis: Dict = {
        'failure_kind': None,
        'failure_location': 'unknown',
        'failing_tests': [],
        'failing_tests_by_category': {},
        'detected_patterns': [],
        'analyzed_files': [],
        'root_cause': {},
        'testsuites': None,
    }

    variant = metadata.get('variant', '')
    if not variant or variant == 'unknown':
        analysis['failure_location'] = identify_failure_location(prowjob_data, base_url, variant)
        analysis['root_cause'] = determine_root_cause([], [], metadata)
        return analysis

    if classification is None:
        classification = classify_testsuites_result(base_url, variant)
    analysis['testsuites'] = classification
    analysis['failure_kind'] = classification['failure_kind']

    detected_patterns: List[Dict] = []
    analyzed_files: List[Dict] = []
    failing_tests: List[Dict] = []
    location_parts: List[str] = []

    # Infra-level patterns (Azure quota, etc.) live in the top-level build-log.txt
    # (ci-operator's own orchestration log) regardless of which case below applies.
    build_log_text: Optional[str] = None
    build_log_content = fetch_artifact(base_url, "build-log.txt")
    if build_log_content:
        try:
            build_log_text = build_log_content.decode('utf-8', errors='ignore')
            scan_text = build_log_text[-100000:] if len(build_log_text) > 100000 else build_log_text
            top_patterns = check_log_for_patterns(scan_text, "build-log.txt")
            detected_patterns.extend(top_patterns)
            analyzed_files.append({
                'name': 'build-log.txt',
                'path': 'build-log.txt',
                'patterns_found': len(top_patterns),
            })
        except Exception as e:
            logger.warning(f"Failed to analyze build-log.txt: {e}")

    if classification['failure_kind'] == 'blocking_phase_failure':
        location_parts = classification['other_failed_steps'] or [
            'pre/test phase (testsuites-gate did not run or failed)'
        ]
    elif classification['failure_kind'] == 'best_effort_suite_failure':
        for suite_key, info in classification['suites'].items():
            if info['status'] != 'FAILURE':
                continue
            suite_result = analyze_suite_failure(base_url, variant, suite_key, info['step_name'])
            classification['suites'][suite_key]['analysis'] = suite_result
            location_parts.append(info['step_name'])
            failing_tests.extend(suite_result['failing_tests'])
            detected_patterns.extend(suite_result['detected_patterns'])
            analyzed_files.append({
                'name': f"{info['step_name']}/build-log.txt",
                'path': f"artifacts/{variant}/{info['step_name']}/build-log.txt",
                'patterns_found': len(suite_result['detected_patterns']),
            })
        location_parts.extend(classification['other_failed_steps'])
    elif classification['failure_kind'] == 'unrelated_post_step_failure':
        location_parts = classification['other_failed_steps']
    else:
        # Classifier found nothing wrong in/around the testsuites chain; fall back to a
        # generic failed-step scan (covers edge cases this classifier doesn't model).
        location_parts = get_failed_steps(base_url, variant) or ['unknown']

    analysis['failure_location'] = ', '.join(location_parts) if location_parts else 'unknown'
    analysis['failing_tests'] = failing_tests
    analysis['failing_tests_by_category'] = categorize_test_list(failing_tests)
    analysis['detected_patterns'] = detected_patterns
    analysis['analyzed_files'] = analyzed_files

    # Determine root cause. failing_tests now comes from real per-suite JUnit
    # (Workstream D) rather than log-scraped case IDs, so there's no
    # extended_build_log_case_ids/oet_build_log_first_test to pass anymore --
    # determine_root_cause() already treats both as optional.
    analysis['root_cause'] = determine_root_cause(
        analysis['failing_tests'],
        analysis['detected_patterns'],
        metadata,
        build_log_text,
    )

    return analysis
