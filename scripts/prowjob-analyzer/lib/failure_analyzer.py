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
    format_hours_minutes_test_step_display,
    parse_junit_xml,
    parse_osc_suite_test_case_info,
)
from .fetcher import (
    ALL_TESTSUITES_STEPS,
    TESTSUITES_GATE_STEP,
    TESTSUITES_SUITE_STEPS,
    _podutils_json_timestamp,
    compute_step_elapsed_display,
    compute_step_elapsed_seconds,
    fetch_artifact,
    fetch_json_artifact,
    get_failed_steps,
    get_step_directories,
    is_cleanup_step,
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
    'go_build_failure': [
        # `go test`'s own output when the package under test fails to
        # *compile* -- distinct from a real test assertion failure. Any
        # testsuites-chain suite backed by `go test` (today: osc, caa) can
        # hit this; it means the suite wrote zero JUnit because nothing ever
        # ran, not that a test executed and failed. See
        # is_test_code_build_failure()/TEST_BUG status below.
        r'FAIL\s+\S+\s+\[build failed\]',
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


def classify_infra_steps(base_url: str, variant: str) -> Dict:
    """
    Split a job's step directories outside the testsuites chain
    (``ALL_TESTSUITES_STEPS``) into "infra" (pre-phase/misc) and "cleanup"
    (gather/deprovision) steps, each with its own currently-failed subset.
    See the prowjob-analyzer plan's result-categories design: ``infra``'s
    status/time only count the former; ``cleanup_failed_steps`` feeds
    ``overall``'s catch-all when a cleanup step is what actually failed.

    Returns:
        {
          'step_dirs': [...],            # every step directory for this variant
          'infra_steps': [...],
          'infra_failed_steps': [...],
          'cleanup_steps': [...],
          'cleanup_failed_steps': [...],
        }
    """
    step_dirs = get_step_directories(base_url, variant)
    failed_steps = set(get_failed_steps(base_url, variant))
    known_steps = set(ALL_TESTSUITES_STEPS)

    infra_steps: List[str] = []
    cleanup_steps: List[str] = []
    for step in step_dirs:
        if step in known_steps:
            continue
        if is_cleanup_step(step):
            cleanup_steps.append(step)
        else:
            infra_steps.append(step)

    return {
        'step_dirs': step_dirs,
        'infra_steps': infra_steps,
        'infra_failed_steps': [s for s in infra_steps if s in failed_steps],
        'cleanup_steps': cleanup_steps,
        'cleanup_failed_steps': [s for s in cleanup_steps if s in failed_steps],
    }


def analyze_steps_failure(base_url: str, variant: str, step_names: List[str]) -> Dict:
    """
    Generalizes :func:`analyze_suite_failure` for arbitrary non-suite steps
    (``infra``'s failed pre-phase steps, or ``overall``'s cleanup-step
    catch-all): fetches each step's own ``build-log.txt``, scans it for
    ``FAILURE_PATTERNS``, and combines every step's detected patterns and
    (truncated) log text into one result for :func:`determine_root_cause`.

    Returns:
        {'detected_patterns': [...], 'build_log_text': '...'}
    """
    combined_patterns: List[Dict] = []
    combined_text_parts: List[str] = []

    for step_name in step_names:
        build_log_path = f"artifacts/{variant}/{step_name}/build-log.txt"
        content = fetch_artifact(base_url, build_log_path)
        if not content:
            continue
        text = content.decode('utf-8', errors='ignore')
        combined_patterns.extend(check_log_for_patterns(text, f"{step_name}/build-log.txt"))
        combined_text_parts.append(text[-100_000:] if len(text) > 100_000 else text)

    return {
        'detected_patterns': combined_patterns,
        'build_log_text': '\n'.join(combined_text_parts),
    }


def _junit_totals(reports: List[Dict]) -> Dict[str, int]:
    """Sum tests/failures/errors/skipped across every JUnit report for one suite."""
    totals = {'tests': 0, 'failures': 0, 'errors': 0, 'skipped': 0}
    for report in reports:
        j = report['junit']
        for key in totals:
            totals[key] += j.get(key, 0)
    totals['passed'] = max(0, totals['tests'] - totals['failures'] - totals['errors'] - totals['skipped'])
    return totals


def is_self_skipped_suite(summary: Dict) -> bool:
    """
    True when a testsuites-chain suite step (osc/kata-upstream/caa) exited
    SUCCESS but its own JUnit shows it never ran a real test: disabled via
    ``TESTS_<SUITE>_ENABLE=false``, or self-skipped because
    ``${SHARED_DIR}/testsuites_gate`` was missing (see
    ``ci-operator/step-registry/sandboxed-containers-operator/testsuites/AGENTS.md``
    conventions 2/3/3b in the ``prow`` repo). Every one of those commands.sh
    paths writes a ``<skipped>`` JUnit testcase and ``exit 0`` -- the step's
    own ``finished.json`` always says SUCCESS in this case (required so
    `best_effort` doesn't block its siblings) -- but that must never be
    reported as a test "pass". ``summarize_suite``'s own JUnit totals
    (``tests``/``skipped``/``failures``/``errors``) are the only signal
    ``prowjob-analyzer`` trusts for this -- never the raw step status alone.
    """
    if not summary.get('had_junit'):
        return False
    tests = summary.get('tests', 0)
    if tests <= 0:
        return False
    return (
        summary.get('skipped', 0) == tests
        and summary.get('failures', 0) == 0
        and summary.get('errors', 0) == 0
    )


def is_test_code_build_failure(summary: Dict) -> bool:
    """
    True when a testsuites-chain suite step (osc/kata-upstream/caa) failed,
    wrote **zero** JUnit at all, and its own build-log.txt shows the
    ``go_build_failure`` pattern (``go test``'s ``FAIL <pkg> [build
    failed]``) -- i.e. the suite's *test code* doesn't compile, rather than
    any test actually running and failing. This is a bug in the test
    repository itself (a stale/removed import, a typo, etc.), not evidence
    the product under test is broken.

    Mirrors :func:`is_self_skipped_suite`'s pattern: a pure function over a
    suite's own summary dict (``had_junit``/``detected_patterns``, both
    already populated by :func:`summarize_suite`), not a raw step-status
    check -- the step's own ``finished.json`` says plain ``FAILURE`` either
    way, same as any other suite failure. ``prowjob-analyzer`` reports this
    case as its own ``TEST_BUG`` status instead of ``FAILURE`` everywhere it
    surfaces a suite's result (result_categories, CSV/JSON, the markdown
    "Test Suites" section) -- and ``overall`` must not inherit a failing
    verdict purely from a TEST_BUG suite either, see
    :func:`build_result_categories`.
    """
    if summary.get('had_junit'):
        return False
    patterns = summary.get('detected_patterns') or []
    return any(p.get('pattern') == 'go_build_failure' for p in patterns)


def extract_go_build_failure_detail(text: str, max_chars: int = 1000) -> Optional[str]:
    """
    Extract the block from the last header before the first build failure.
    Falls back to the failure line when there is no preceding header. This
    returns untrusted log content and must not be included in reports.
    """
    if not text:
        return None
    fail_line = re.search(r'^FAIL\s+\S+\s+\[build failed\]\s*$', text, re.MULTILINE)
    if not fail_line:
        return None
    headers = list(re.finditer(r'^#[ \t]+\S.*$', text[:fail_line.start()], re.MULTILINE))
    start = headers[-1].start() if headers else fail_line.start()
    detail = text[start:fail_line.end()].strip()
    return detail[:max_chars]


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
        'build_failure_detail': None,
        'is_test_bug': False,
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
        base['build_failure_detail'] = suite_result.get('build_failure_detail')
        base['is_test_bug'] = is_test_code_build_failure(base)

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
    build_failure_detail: Optional[str] = None
    if build_log_content:
        build_log_text = build_log_content.decode('utf-8', errors='ignore')
        detected_patterns = check_log_for_patterns(build_log_text, f"{step_name}/build-log.txt")
        # Do not copy raw compiler output to reports: build logs can contain
        # credentials or other sensitive data. The artifact link is retained.

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
        'build_failure_detail': build_failure_detail,
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
    build_failure_detail: Optional[str] = None,
    test_code_build_failure: bool = False,
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
        build_failure_detail: Deprecated; raw log content is not reported.
        test_code_build_failure: The caller confirmed a suite compile failure
            with no JUnit, and there are no other failures being diagnosed.

    Returns:
        Dictionary with root cause analysis
    """
    root_cause = {
        'primary_pattern': None,
        'likely_cause': '',
        'confidence': 'low',
        'suggested_actions': [],
    }

    # A build-failure string in an aggregate log or a suite with JUnit does
    # not establish that the failure being diagnosed is a test-code bug.
    if not test_code_build_failure:
        detected_patterns = [p for p in detected_patterns if p.get('pattern') != 'go_build_failure']

    # Extract just pattern names for easier checking
    pattern_names = [p['pattern'] for p in detected_patterns]

    # Function to find source of a specific pattern
    def find_pattern_source(pattern_name: str) -> Optional[str]:
        for pattern_info in detected_patterns:
            if pattern_info['pattern'] == pattern_name:
                return pattern_info['source']
        return None

    # Only a caller with suite-level evidence may attribute the failure to
    # test code. Do not reproduce untrusted compiler output in reports.
    if 'go_build_failure' in pattern_names:
        root_cause['primary_pattern'] = 'go_build_failure'
        root_cause['pattern_source'] = find_pattern_source('go_build_failure')
        root_cause['confidence'] = 'high'
        root_cause['likely_cause'] = (
            "Test code fails to compile (test-infrastructure bug, not a "
            "product defect); see the suite step's build-log.txt for the "
            "exact compiler error."
        )
        root_cause['suggested_actions'] = [
            "Fix the compile error in the test suite's own source (missing "
            "import, undefined symbol, etc.)",
            'This is not a KataConfig/CAA/Trustee product regression -- do '
            'not file it as one',
        ]
        return root_cause

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
    failed_suite_results: List[Dict] = []

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
            failed_suite_results.append(suite_result)
            classification['suites'][suite_key]['analysis'] = suite_result
            # Additive (prowjob-analyzer plan, result-categories): each
            # failing suite also gets its own real root-cause dict, rather
            # than only contributing to the pooled call below -- this is
            # what lets a future per-suite consumer see e.g. "osc failed
            # because of X" independently of "kata-upstream failed because
            # of Y" instead of one blended answer for the whole job. The
            # pooled failing_tests/detected_patterns/root_cause computed
            # below are unchanged, for backward compatibility of today's
            # failure_analysis.root_cause JSON key.
            classification['suites'][suite_key]['root_cause'] = determine_root_cause(
                suite_result['failing_tests'],
                suite_result['detected_patterns'],
                metadata,
                test_code_build_failure=is_test_code_build_failure(suite_result),
            )
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
        test_code_build_failure=(
            bool(failed_suite_results)
            and all(is_test_code_build_failure(result) for result in failed_suite_results)
            and not classification.get('other_failed_steps')
        ),
    )

    return analysis


# Every result-categories entry (see build_result_categories()) is this
# same shape, blank fields by default -- a category only fills in
# failed_steps/primary_pattern/confidence/root_cause when it actually
# failed (see the prowjob-analyzer plan, §2's blank-vs-"not run" rule).
_EMPTY_CATEGORY: Dict = {
    'status': '',
    'time': '',
    'failed_steps': '',
    'primary_pattern': '',
    'confidence': '',
    'root_cause': '',
}


def _new_category() -> Dict:
    return dict(_EMPTY_CATEGORY)


def _build_infra_category(base_url: str, variant: str, metadata: Dict, infra_info: Dict) -> Dict:
    """``infra``: pre-phase/misc steps outside the testsuites chain, excluding cleanup."""
    cat = _new_category()
    step_dirs = infra_info.get('step_dirs') or []

    if not variant or variant == 'unknown' or not step_dirs:
        cat['status'] = 'not run'
        return cat

    infra_steps = infra_info.get('infra_steps') or []
    infra_failed = infra_info.get('infra_failed_steps') or []

    cat['status'] = 'FAILURE' if infra_failed else 'SUCCESS'

    total_secs = 0
    for step in infra_steps:
        secs = compute_step_elapsed_seconds(base_url, variant, step)
        if secs:
            total_secs += secs
    cat['time'] = format_hours_minutes_test_step_display(total_secs)

    if infra_failed:
        cat['failed_steps'] = ', '.join(infra_failed)
        steps_result = analyze_steps_failure(base_url, variant, infra_failed)
        rc = determine_root_cause(
            [], steps_result['detected_patterns'], metadata, steps_result['build_log_text']
        )
        cat['primary_pattern'] = rc.get('primary_pattern') or ''
        cat['confidence'] = rc.get('confidence') or ''
        cat['root_cause'] = rc.get('likely_cause') or ''

    return cat


def _build_overall_category(
    prowjob_data: Dict,
    base_url: str,
    variant: str,
    metadata: Dict,
    status: str,
    prowjob_elapsed_time: str,
    infra_info: Dict,
) -> Dict:
    """``overall``: the whole Prow job's own verdict (today's ``prowjob_status``)."""
    cat = _new_category()
    cat['status'] = (status or '').upper()
    cat['time'] = prowjob_elapsed_time or ''

    if cat['status'] == 'SUCCESS':
        return cat

    state = (prowjob_data.get('state') or '').lower()

    if state == 'aborted':
        cat['failed_steps'] = 'aborted'
        cat['root_cause'] = 'The job was aborted.'
    elif 'timeout' in state:
        cat['failed_steps'] = 'timeout'
        cat['root_cause'] = 'The job exceeded its execution timeout.'
    elif state in ('error', 'errored'):
        cat['failed_steps'] = 'infrastructure'
        cat['root_cause'] = 'The job failed due to infrastructure issues.'
    elif state == 'failure':
        cleanup_failed = infra_info.get('cleanup_failed_steps') or []
        if cleanup_failed and variant and variant != 'unknown':
            cat['failed_steps'] = ', '.join(cleanup_failed)
            steps_result = analyze_steps_failure(base_url, variant, cleanup_failed)
            rc = determine_root_cause(
                [], steps_result['detected_patterns'], metadata, steps_result['build_log_text']
            )
            cat['primary_pattern'] = rc.get('primary_pattern') or ''
            cat['confidence'] = rc.get('confidence') or ''
            cat['root_cause'] = rc.get('likely_cause') or ''
        else:
            cat['root_cause'] = 'unknown'
    else:
        cat['root_cause'] = 'unknown'

    return cat


def _build_smoke_category(
    base_url: str, variant: str, metadata: Dict, classification: Dict, suite_summaries: Dict,
) -> Dict:
    """``smoke``: the ``sandboxed-containers-operator-testsuites-gate`` step."""
    cat = _new_category()
    gate_exists = classification.get('gate_step_exists', False)

    if not gate_exists:
        cat['status'] = 'not run'
        return cat

    cat['status'] = classification.get('gate_status') or ''
    gate_summary = (suite_summaries or {}).get('gate') or {}
    cat['time'] = gate_summary.get('elapsed_time') or ''

    if cat['status'] == 'FAILURE' and variant and variant != 'unknown':
        cat['failed_steps'] = TESTSUITES_GATE_STEP
        content = fetch_artifact(base_url, f"artifacts/{variant}/{TESTSUITES_GATE_STEP}/build-log.txt")
        patterns: List[Dict] = []
        log_text = ''
        if content:
            text = content.decode('utf-8', errors='ignore')
            patterns = check_log_for_patterns(text, f"{TESTSUITES_GATE_STEP}/build-log.txt")
            log_text = text[-100_000:] if len(text) > 100_000 else text
        rc = determine_root_cause([], patterns, metadata, log_text)
        cat['primary_pattern'] = rc.get('primary_pattern') or ''
        cat['confidence'] = rc.get('confidence') or ''
        cat['root_cause'] = rc.get('likely_cause') or ''

    return cat


def _kata_upstream_failed_tests(failing_tests: List[Dict]) -> List[str]:
    """Describe failing BATS cases using the runner's per-file JUnit names."""
    prefix = 'junit_kata_upstream_'
    identifiers = []
    for test in failing_tests:
        name = (test.get('name') or '').strip() or '<unnamed test>'
        junit_file = test.get('junit_file') or ''
        if junit_file.startswith(prefix) and junit_file.endswith('.xml'):
            stem = junit_file[len(prefix):-len('.xml')]
            # The step also writes a synthetic skip artifact, not a BATS file.
            if stem and stem != 'skip' and '/' not in stem and '\\' not in stem:
                bats_file = stem if stem.endswith('.bats') else f'{stem}.bats'
                identifiers.append(f'{bats_file}: {name}')
                continue
        identifiers.append(f'{name} (BATS filename unavailable)')
    return identifiers


def _caa_failed_tests(failing_tests: List[Dict]) -> List[str]:
    """Return Go test names, excluding synthetic suite-level JUnit cases."""
    names = []
    for test in failing_tests:
        if test.get('junit_file') in ('junit_caa_noresults.xml', 'junit_caa_skip.xml'):
            continue
        name = (test.get('name') or '').strip()
        if name:
            names.append(name)
    return names


def _build_suite_category(
    metadata: Dict,
    suite_key: str,
    step_name: str,
    classification: Dict,
    suite_summaries: Dict,
) -> Dict:
    """One block per :data:`TESTSUITES_SUITE_STEPS` entry (today: osc/kata-upstream/caa)."""
    cat = _new_category()
    info = (classification.get('suites') or {}).get(suite_key) or {}
    step_exists = info.get('step_exists', False)

    if not step_exists:
        cat['status'] = 'not run'
        return cat

    cat['status'] = info.get('status') or ''
    summary = (suite_summaries or {}).get(suite_key) or {}
    cat['time'] = summary.get('elapsed_time') or ''

    if cat['status'] == 'SUCCESS' and is_self_skipped_suite(summary):
        # The step itself exited 0 (required for best_effort), but its JUnit
        # shows zero real tests ran -- a disabled or self-skipped suite must
        # never be reported as SUCCESS (see is_self_skipped_suite).
        cat['status'] = 'skipped'
    elif cat['status'] == 'FAILURE':
        cat['failed_steps'] = step_name
        if suite_key == 'kata-upstream':
            failed_tests = _kata_upstream_failed_tests(summary.get('failing_tests') or [])
        elif suite_key == 'caa':
            failed_tests = _caa_failed_tests(summary.get('failing_tests') or [])
        else:
            failed_tests = []
        if failed_tests:
            cat['failed_steps'] += f" (failed tests: {'; '.join(failed_tests)})"
        rc = determine_root_cause(
            summary.get('failing_tests') or [],
            summary.get('detected_patterns') or [],
            metadata,
            test_code_build_failure=is_test_code_build_failure(summary),
        )
        cat['primary_pattern'] = rc.get('primary_pattern') or ''
        cat['confidence'] = rc.get('confidence') or ''
        cat['root_cause'] = rc.get('likely_cause') or ''
        if is_test_code_build_failure(summary):
            # The suite's test code failed to compile -- a test-infrastructure
            # bug, not evidence the product under test is broken. Reported as
            # its own TEST_BUG status instead of FAILURE (see
            # is_test_code_build_failure()); failed_steps/root_cause above
            # still describe exactly what failed and why.
            cat['status'] = 'TEST_BUG'

    return cat


def build_result_categories(
    prowjob_data: Dict,
    base_url: str,
    metadata: Dict,
    status: str,
    prowjob_elapsed_time: str,
    classification: Optional[Dict],
    suite_summaries: Optional[Dict],
) -> Dict[str, Dict]:
    """
    Top-level orchestrator for the per-category SUCCESS/FAILURE breakdown
    described in the prowjob-analyzer plan: ``infra``, ``overall``,
    ``smoke``, then one entry per :data:`TESTSUITES_SUITE_STEPS` key (today
    ``osc``/``kata_upstream``/``caa``), in that fixed order -- always, for
    every job, regardless of which categories passed, failed, or are "not
    run"/"skipped" (see the plan's §6.2 fixed-layout requirement). Each
    entry is a plain dict: ``{status, time, failed_steps, primary_pattern,
    confidence, root_cause}``.

    When ``infra``'s own status is ``FAILURE``, every other category is
    reported as ``'skipped'`` rather than its own independent result:
    ci-operator skips the test:-phase gate step whenever the pre: phase
    fails, and in practice the post:-phase testsuites chain doesn't produce
    a meaningful result either in that case -- so once infra itself is
    broken, ``overall``/``smoke``/every suite are treated as skipped instead
    of evaluating (and fetching build-logs for) whatever each one's own
    ``finished.json`` happens to say.

    Reuses ``classification``/``suite_summaries`` (already computed once by
    dig.py via :func:`classify_testsuites_result`/:func:`summarize_all_suites`)
    rather than re-fetching; only ``infra``/``overall``/``smoke`` perform any
    *new* fetching here (one ``build-log.txt`` per failed non-suite step),
    and none of that fetching happens at all once infra has already failed.

    A suite category's status can also be ``'TEST_BUG'`` instead of
    ``'FAILURE'``: the suite's own test code failed to *compile* (see
    :func:`is_test_code_build_failure`), not a real test assertion failure,
    so it isn't evidence the product under test is broken. ``overall`` must
    not inherit a false ``FAILURE`` verdict purely from that case either --
    a ``TEST_BUG`` suite is treated exactly like a self-skipped suite for
    ``overall``'s own determination (it doesn't count as a real failure).
    When every testsuites-chain suite contributing to the job's own
    ``FAILURE`` state is a ``TEST_BUG`` (and nothing else, e.g. ``smoke`` or
    a cleanup step, also failed), ``overall`` is recomputed: a JUnit-confirmed
    passing test gives ``'SUCCESS'``; if all suites are TEST_BUG or skipped,
    it is ``'skipped'``. Missing or inconclusive suite results give
    ``'unknown'`` rather than being assumed to be skipped or passed.

    This is the single source of truth for "what happened in this job,
    categorized" -- ``lib.report_generator.build_report_data`` stores this
    return value verbatim as ``report_data['result_categories']``, and every
    output format (JSON/CSV/markdown) reads from that one dict rather than
    recomputing anything (see the plan's §6.1).
    """
    classification = classification or {}
    suite_summaries = suite_summaries or {}
    variant = metadata.get('variant') or ''

    if variant and variant != 'unknown':
        infra_info = classify_infra_steps(base_url, variant)
    else:
        infra_info = {
            'step_dirs': [], 'infra_steps': [], 'infra_failed_steps': [],
            'cleanup_steps': [], 'cleanup_failed_steps': [],
        }

    categories: Dict[str, Dict] = {}
    categories['infra'] = _build_infra_category(base_url, variant, metadata, infra_info)

    if categories['infra']['status'] == 'FAILURE':
        skipped = _new_category()
        skipped['status'] = 'skipped'
        categories['overall'] = dict(skipped)
        categories['smoke'] = dict(skipped)
        for suite_key in TESTSUITES_SUITE_STEPS:
            categories[suite_key.replace('-', '_')] = dict(skipped)
        return categories

    categories['overall'] = _build_overall_category(
        prowjob_data, base_url, variant, metadata, status, prowjob_elapsed_time, infra_info
    )
    categories['smoke'] = _build_smoke_category(
        base_url, variant, metadata, classification, suite_summaries
    )

    # One entry per TESTSUITES_SUITE_STEPS, in its (append-only) insertion
    # order -- never hardcoded osc/kata_upstream/caa names -- so adding a
    # suite there is a one-line config change (see the plan, §2.1).
    for suite_key, step_name in TESTSUITES_SUITE_STEPS.items():
        field = suite_key.replace('-', '_')
        categories[field] = _build_suite_category(
            metadata, suite_key, step_name, classification, suite_summaries
        )

    # overall must not inherit a FAILURE verdict purely from a TEST_BUG suite
    # (a test-code compile bug, not a product regression -- see
    # is_test_code_build_failure()). A TEST_BUG suite is treated exactly like
    # a self-skipped suite for overall's own determination: it doesn't count
    # as a real failure. Only when every testsuites-chain suite that
    # raw-FAILUREd is a test bug, and nothing else (smoke, an unrelated
    # post-step/cleanup step) also contributed to the job's own FAILURE
    # state, does this apply -- if anything else genuinely failed, overall
    # correctly stays FAILURE.
    if (
        categories['overall']['status'] == 'FAILURE'
        and classification.get('failure_kind') == 'best_effort_suite_failure'
        and not classification.get('other_failed_steps')
        and categories['smoke']['status'] != 'FAILURE'
    ):
        failing_suite_keys = [
            k for k, info in (classification.get('suites') or {}).items()
            if info.get('status') == 'FAILURE'
        ]
        if failing_suite_keys and all(
            is_test_code_build_failure((suite_summaries or {}).get(k) or {})
            for k in failing_suite_keys
        ):
            # A successful process is not evidence of a passing test when
            # JUnit is missing, invalid or contains no passing cases.
            any_real_pass = any(
                categories[k.replace('-', '_')]['status'] == 'SUCCESS'
                and (suite_summaries.get(k) or {}).get('had_junit', False)
                and (suite_summaries.get(k) or {}).get('passed', 0) > 0
                for k in TESTSUITES_SUITE_STEPS
            )
            all_skipped = all(
                categories[k.replace('-', '_')]['status'] in ('TEST_BUG', 'skipped')
                for k in TESTSUITES_SUITE_STEPS
            )
            categories['overall']['status'] = (
                'SUCCESS' if any_real_pass else 'skipped' if all_skipped else 'unknown'
            )
            categories['overall']['failed_steps'] = ''
            categories['overall']['primary_pattern'] = ''
            categories['overall']['confidence'] = ''
            categories['overall']['root_cause'] = ''

    return categories
