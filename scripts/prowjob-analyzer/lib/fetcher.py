"""
Artifact Fetcher Module

Handles URL parsing and fetching artifacts from Prow/GCS.
"""

import re
import time
import json
import logging
from typing import Dict, Optional, Tuple, List
from urllib.request import urlopen, Request
from urllib.error import HTTPError, URLError

logger = logging.getLogger(__name__)


def parse_prow_url(url: str) -> Tuple[str, str, str]:
    """
    Parse a Prow job URL to extract job name and build ID.
    Supports both periodic and presubmit (PR rehearsal) job URLs.

    Args:
        url: Prow UI URL (https://prow.ci.openshift.org/view/gs/...)

    Returns:
        Tuple of (base_url, job_name, build_id)

    Raises:
        ValueError: If URL format is invalid
    """
    # Try presubmit/PR pattern first (more specific)
    # Format: https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/{ORG}_{REPO}/{PR_NUMBER}/{JOB_NAME}/{BUILD_ID}
    pr_pattern = r'https://prow\.ci\.openshift\.org/view/gs/([^/]+)/pr-logs/pull/([^/]+)/([^/]+)/([^/]+)/([^/\?]+)'
    match = re.match(pr_pattern, url)

    if match:
        bucket = match.group(1)
        org_repo = match.group(2)
        pr_number = match.group(3)
        job_name = match.group(4)
        build_id = match.group(5)

        # Construct base URL for presubmit jobs
        base_url = f"https://prow.ci.openshift.org/view/gs/{bucket}/pr-logs/pull/{org_repo}/{pr_number}/{job_name}/{build_id}"
        logger.debug(f"Parsed as presubmit job: PR {org_repo}#{pr_number}")
        return base_url, job_name, build_id

    # Try periodic/postsubmit pattern
    # Format: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/{JOB_NAME}/{BUILD_ID}
    periodic_pattern = r'https://prow\.ci\.openshift\.org/view/gs/([^/]+)/([^/]+)/([^/]+)/([^/\?]+)'
    match = re.match(periodic_pattern, url)

    if match:
        bucket = match.group(1)
        log_type = match.group(2)
        job_name = match.group(3)
        build_id = match.group(4)

        # Construct base URL for periodic/postsubmit jobs
        base_url = f"https://prow.ci.openshift.org/view/gs/{bucket}/{log_type}/{job_name}/{build_id}"
        logger.debug(f"Parsed as periodic/postsubmit job")
        return base_url, job_name, build_id

    # No pattern matched
    raise ValueError(
        f"Invalid Prow URL format. Expected one of:\n"
        f"  Periodic: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/{{JOB_NAME}}/{{BUILD_ID}}\n"
        f"  Presubmit: https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/{{ORG}}_{{REPO}}/{{PR}}/{{JOB_NAME}}/{{BUILD_ID}}\n"
        f"Got: {url}"
    )


def extract_variant_from_job_name(job_name: str) -> Optional[str]:
    """
    Extract the variant from job name for artifact path construction.

    For OSC jobs, the variant is typically: {provider}-ipi-{workload}
    Example: periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods
    Variant: aws-ipi-peerpods

    Args:
        job_name: Prow job name

    Returns:
        Variant string or None if not found
    """
    # Look for known patterns in OSC job names
    # Pattern: ...downstream-candidate-{VARIANT}
    pattern = r'downstream-candidate([0-9]+)?-(.+)$'
    match = re.search(pattern, job_name)

    if match:
        return match.group(2)

    # Try alternative patterns for other job types
    # Pattern: ...{provider}-ipi-{workload}
    providers = ['aro', 'aws', 'azure', 'gcp', 'ibmcloud', 'vsphere']
    for provider in providers:
        pattern = f'({provider}-ipi-[^-]+)'
        match = re.search(pattern, job_name)
        if match:
            return match.group(1)

    logger.warning(f"Could not extract variant from job name: {job_name}")
    return None


def discover_gcs_url_from_prow(prow_url: str) -> Optional[str]:
    """
    Discover the actual GCS web URL by parsing Prow's HTML response.

    Args:
        prow_url: Prow view URL that may return HTML

    Returns:
        Discovered GCS web URL, or None if not found
    """
    try:
        req = Request(prow_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
        with urlopen(req, timeout=30) as response:
            content_type = response.headers.get('Content-Type', '')

            if 'text/html' in content_type:
                # Parse HTML to extract GCS web URL
                html = response.read().decode('utf-8', errors='ignore')
                # Look for gcsweb URL in the HTML
                match = re.search(r'https://[^"\s]*gcsweb[^"\s]*', html)
                if match:
                    gcs_url = match.group(0).rstrip('/')
                    logger.debug(f"Discovered GCS URL: {gcs_url}")
                    return gcs_url

    except Exception as e:
        logger.debug(f"Failed to discover GCS URL from {prow_url}: {e}")

    return None


def fetch_artifact(base_url: str, artifact_path: str, max_retries: int = 3) -> Optional[bytes]:
    """
    Fetch an artifact from Prow/GCS with retry logic.
    Dynamically discovers the GCS web URL if Prow returns HTML.

    Args:
        base_url: Base URL of the Prow job
        artifact_path: Relative path to the artifact (e.g., "prowjob.json")
        max_retries: Maximum number of retry attempts

    Returns:
        Artifact content as bytes, or None if not found
    """
    url = f"{base_url}/{artifact_path}"

    for attempt in range(max_retries):
        try:
            logger.debug(f"Fetching {url} (attempt {attempt + 1}/{max_retries})")
            req = Request(url, headers={'User-Agent': 'prowjob-analyzer/1.0'})

            with urlopen(req, timeout=30) as response:
                content_type = response.headers.get('Content-Type', '')
                content = response.read()

                # Check if we got HTML instead of the actual artifact
                if 'text/html' in content_type and attempt == 0:
                    logger.debug(f"Got HTML response, discovering GCS URL...")
                    # Try to discover the actual GCS URL from the HTML
                    gcs_url = discover_gcs_url_from_prow(url)
                    if gcs_url:
                        # Retry with the discovered GCS URL
                        logger.debug(f"Retrying with discovered GCS URL: {gcs_url}")
                        req = Request(gcs_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
                        with urlopen(req, timeout=30) as gcs_response:
                            gcs_content_type = gcs_response.headers.get('Content-Type', '')
                            content = gcs_response.read()

                            # If we still got HTML from GCS, the file doesn't exist
                            if 'text/html' in gcs_content_type:
                                logger.debug(f"GCS also returned HTML - artifact not found")
                                return None

                            logger.debug(f"Successfully fetched from GCS ({len(content)} bytes)")
                            return content
                    else:
                        # Could not discover GCS URL and got HTML - file doesn't exist
                        logger.debug(f"Could not discover GCS URL - artifact not found")
                        return None

                # If we got HTML after retry or without Prow redirect, file doesn't exist
                if 'text/html' in content_type:
                    logger.debug(f"Got HTML response - artifact not found")
                    return None

                logger.debug(f"Successfully fetched {url} ({len(content)} bytes)")
                return content

        except HTTPError as e:
            if e.code == 404:
                logger.debug(f"Artifact not found: {url}")
                return None
            elif e.code in (500, 502, 503, 504) and attempt < max_retries - 1:
                wait_time = 2 ** attempt  # Exponential backoff
                logger.warning(f"Server error {e.code}, retrying in {wait_time}s...")
                time.sleep(wait_time)
            else:
                logger.error(f"HTTP error fetching {url}: {e.code} {e.reason}")
                return None

        except URLError as e:
            if attempt < max_retries - 1:
                wait_time = 2 ** attempt
                logger.warning(f"Network error, retrying in {wait_time}s: {e}")
                time.sleep(wait_time)
            else:
                logger.error(f"Failed to fetch {url}: {e}")
                return None

        except Exception as e:
            logger.error(f"Unexpected error fetching {url}: {e}")
            return None

    return None


def fetch_json_artifact(base_url: str, artifact_path: str) -> Optional[Dict]:
    """
    Fetch and parse a JSON artifact.

    Args:
        base_url: Base URL of the Prow job
        artifact_path: Relative path to the JSON artifact

    Returns:
        Parsed JSON as dict, or None if not found or invalid
    """
    content = fetch_artifact(base_url, artifact_path)
    if content is None:
        return None

    try:
        return json.loads(content.decode('utf-8'))
    except (json.JSONDecodeError, UnicodeDecodeError) as e:
        logger.error(f"Failed to parse JSON from {artifact_path}: {e}")
        return None


def check_artifacts_ready(base_url: str) -> bool:
    """
    Check if job artifacts are ready (job has finished).

    Args:
        base_url: Base URL of the Prow job

    Returns:
        True if artifacts are ready, False otherwise
    """
    # Check for finished.json which indicates the job has completed
    finished = fetch_json_artifact(base_url, "finished.json")
    return finished is not None


def wait_for_artifacts(base_url: str, timeout: int = 300, poll_interval: int = 10) -> bool:
    """
    Wait for job artifacts to become available.

    Args:
        base_url: Base URL of the Prow job
        timeout: Maximum time to wait in seconds (default: 5 minutes)
        poll_interval: Time between polls in seconds (default: 10 seconds)

    Returns:
        True if artifacts became ready, False if timeout
    """
    start_time = time.time()

    logger.info(f"Waiting for job artifacts (timeout: {timeout}s)...")

    while (time.time() - start_time) < timeout:
        if check_artifacts_ready(base_url):
            elapsed = time.time() - start_time
            logger.info(f"Artifacts ready after {elapsed:.1f}s")
            return True

        remaining = timeout - (time.time() - start_time)
        logger.info(f"Job still running, checking again in {poll_interval}s (timeout in {remaining:.0f}s)...")
        time.sleep(poll_interval)

    logger.warning(f"Timeout waiting for artifacts after {timeout}s")
    return False


def get_artifact_url(base_url: str, artifact_path: str) -> str:
    """
    Generate a GCS web URL for an artifact.

    Args:
        base_url: Base URL of the Prow job
        artifact_path: Relative path to the artifact

    Returns:
        Full URL to the artifact
    """
    return f"{base_url}/{artifact_path}"


def get_step_directories(base_url: str, variant: str) -> List[str]:
    """
    Get list of step directories from artifacts.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant (e.g., "aws-ipi-peerpods")

    Returns:
        List of step directory names
    """
    artifacts_url = f"{base_url}/artifacts/{variant}/"

    try:
        logger.debug(f"Fetching step directories from {artifacts_url}")
        req = Request(artifacts_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
        with urlopen(req, timeout=30) as response:
            content_type = response.headers.get('Content-Type', '')
            html_content = response.read().decode('utf-8', errors='ignore')
            logger.debug(f"Fetched {len(html_content)} bytes, content-type: {content_type}")

            # Check if we got HTML redirect page instead of directory listing
            if 'text/html' in content_type and 'gcsweb' not in html_content[:1000]:
                logger.debug("Got Prow HTML page, discovering GCS URL...")
                gcs_url = discover_gcs_url_from_prow(artifacts_url)
                if gcs_url:
                    logger.debug(f"Retrying with GCS URL: {gcs_url}")
                    req = Request(gcs_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
                    with urlopen(req, timeout=30) as gcs_response:
                        html_content = gcs_response.read().decode('utf-8', errors='ignore')
                        logger.debug(f"Fetched {len(html_content)} bytes from GCS")

            # Extract directory names from HTML listing
            # Pattern: href="/gcs/.../artifacts/{variant}/STEP_NAME/"
            # We want to extract just the STEP_NAME part
            pattern = rf'href="[^"]*/{variant}/([a-z0-9-]+)/"'
            matches = re.findall(pattern, html_content)
            logger.debug(f"Regex pattern matched {len(matches)} times")

            # Remove duplicates and filter out parent directory references
            step_dirs = []
            seen = set()
            for m in matches:
                if m not in seen and m not in ['..', 'artifacts']:
                    step_dirs.append(m)
                    seen.add(m)

            logger.debug(f"Found {len(step_dirs)} step directories in {variant}: {step_dirs[:5]}")
            return step_dirs

    except Exception as e:
        logger.warning(f"Failed to get step directories from {artifacts_url}: {e}")
        return []


def summarize_openshift_extended_test_build_log(log_text: str) -> Optional[str]:
    """
    Build a short label for openshift-extended-test from build-log.txt.

    - If the step finished with a test failure: ``error: X fail, V pass, W skip`` →
      ``"X fail, V pass, W skip"``.
    - If tests were interrupted (no final error line): last ``started: (X/Y/Z)`` →
      ``"(X/Y/Z)"`` (X failed, Y started, Z total).
    """
    if not log_text:
        return None

    error_re = re.compile(
        r'(?i)error:\s*(\d+)\s*fail,\s*(\d+)\s*pass,\s*(\d+)\s*skip',
    )
    err_matches = list(error_re.finditer(log_text))
    if err_matches:
        m = err_matches[-1]
        return f"{m.group(1)} fail, {m.group(2)} pass, {m.group(3)} skip"

    started_re = re.compile(
        r'(?i)started:\s*\(\s*(\d+)\s*/\s*(\d+)\s*/\s*(\d+)\s*\)',
    )
    started_matches = list(started_re.finditer(log_text))
    if started_matches:
        m = started_matches[-1]
        return f"({m.group(1)}/{m.group(2)}/{m.group(3)})"

    return None


def is_openshift_extended_test_failure_label(step_label: str) -> bool:
    """
    True if this string is the openshift-extended-test step or a build-log summary
    derived from that step (instead of the bare directory name).
    """
    if step_label == 'openshift-extended-test':
        return True
    if re.fullmatch(r'\d+ fail, \d+ pass, \d+ skip', step_label):
        return True
    if re.fullmatch(r'\(\d+/\d+/\d+\)', step_label):
        return True
    return False


def artifact_dir_for_failed_step_label(step_label: str) -> str:
    """Directory under artifacts/{variant}/ for linking (summaries map to openshift-extended-test)."""
    if is_openshift_extended_test_failure_label(step_label):
        return 'openshift-extended-test'
    return step_label


def get_failed_steps(base_url: str, variant: str) -> List[str]:
    """
    Identify which steps failed by checking their finished.json files.

    Special handling for openshift-extended-test: Also checks test-results.yaml
    since this step may report as passed even when tests failed.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant (e.g., "aws-ipi-peerpods")

    Returns:
        List of failed step labels. For openshift-extended-test, this may be a summary
        from build-log.txt (e.g. ``3 fail, 10 pass, 2 skip`` or ``(0/5/40)``) instead of
        the bare step name.
    """
    step_dirs = get_step_directories(base_url, variant)
    failed_steps = []

    for step in step_dirs:
        finished_path = f"artifacts/{variant}/{step}/finished.json"
        finished_data = fetch_json_artifact(base_url, finished_path)

        if finished_data:
            # Check if step failed according to finished.json
            passed = finished_data.get('passed', True)
            result = finished_data.get('result', '').upper()

            step_failed = not passed or result == 'FAILURE'

            # Special case: openshift-extended-test may report as passed
            # even when tests failed. Check test-results.yaml to be sure.
            if step == 'openshift-extended-test' and not step_failed:
                test_results_path = f"artifacts/{variant}/{step}/artifacts/test-results.yaml"
                test_results_content = fetch_artifact(base_url, test_results_path)

                if test_results_content:
                    # Import here to avoid circular dependency
                    from .parser import parse_test_results

                    test_results = parse_test_results(test_results_content)
                    if test_results:
                        failures = test_results.get('failures', 0)
                        errors = test_results.get('errors', 0)

                        if failures > 0 or errors > 0:
                            step_failed = True
                            logger.debug(f"Step {step} has test failures: failures={failures}, errors={errors}")

            if step_failed:
                if step == 'openshift-extended-test':
                    label: Optional[str] = None
                    bl_path = f"artifacts/{variant}/{step}/build-log.txt"
                    bl_content = fetch_artifact(base_url, bl_path)
                    if bl_content:
                        try:
                            label = summarize_openshift_extended_test_build_log(
                                bl_content.decode('utf-8', errors='ignore')
                            )
                        except Exception as e:
                            logger.debug(f"Could not summarize openshift-extended-test build-log: {e}")
                    failed_steps.append(label or step)
                    logger.debug(
                        "Step %s failed: passed=%s, result=%s, label=%s",
                        step, passed, result, label or step,
                    )
                else:
                    failed_steps.append(step)
                    logger.debug(f"Step {step} failed: passed={passed}, result={result}")

    logger.debug(f"Found {len(failed_steps)} failed steps: {failed_steps}")
    return failed_steps


def _podutils_json_timestamp(obj: Optional[Dict]) -> Optional[int]:
    """Epoch seconds from a Prow step ``started.json`` / ``finished.json``."""
    if not isinstance(obj, dict):
        return None
    t = obj.get('timestamp')
    if t is None:
        return None
    try:
        return int(float(t))
    except (TypeError, ValueError):
        return None


def go_duration_to_seconds(s: str) -> Optional[int]:
    """Parse a Go-style duration fragment (e.g. ``1h30m5s``, ``45m0s``) to seconds."""
    s = (s or '').strip()
    if not s:
        return None
    total = 0.0
    # Milliseconds before ``m`` (minutes): ``813ms`` must not match as 813 minutes.
    for m in re.finditer(r'(\d+(?:\.\d+)?)ms', s):
        total += float(m.group(1)) / 1000.0
    s = re.sub(r'(\d+(?:\.\d+)?)ms', ' ', s)
    for m in re.finditer(r'(\d+(?:\.\d+)?)h', s):
        total += float(m.group(1)) * 3600
    for m in re.finditer(r'(\d+(?:\.\d+)?)m', s):
        total += float(m.group(1)) * 60
    for m in re.finditer(r'(\d+(?:\.\d+)?)s', s):
        total += float(m.group(1))
    if total <= 0:
        return None
    return int(round(total))


def parse_test_step_duration_from_build_log(text: str) -> Optional[int]:
    """
    Duration from ``error: X fail, V pass, W skip (DURATION)`` in build-log.txt
    (openshift-tests suite summary). When this line appears, use the last match as the
    step elapsed time; it overrides per-test ``failed: (…)`` durations elsewhere in the log.
    """
    if not text:
        return None
    matches = list(
        re.finditer(
            r'(?i)error:\s*\d+\s*fail,\s*\d+\s*pass,\s*\d+\s*skip\s*\(\s*([^)]+)\s*\)',
            text,
        )
    )
    if not matches:
        return None
    return go_duration_to_seconds(matches[-1].group(1))


def parse_ginkgo_failed_durations_max(text: str) -> Optional[int]:
    """
    Longest ``failed: (3h1m37s)``-style duration from ginkgo output (per-test timeout).
    """
    if not text:
        return None
    best: Optional[int] = None
    for m in re.finditer(r'(?im)^\s*failed:\s*\(\s*([^)]+)\s*\)', text):
        secs = go_duration_to_seconds(m.group(1).strip())
        if secs is not None and secs > 0:
            if best is None or secs > best:
                best = secs
    return best


def _line_is_prow_entrypoint_noise(line: str) -> bool:
    """Skip Prow sidecar JSON lines (grace period, global timeout) — not suite duration."""
    s = line.lower()
    if '"component"' in s and 'entrypoint' in s:
        return True
    if 'grace period' in s or 'gracefullyterminate' in s:
        return True
    if 'did not finish before' in s and 'timeout' in s:
        return True
    return False


def parse_build_log_duration_fallback(text: str) -> Optional[int]:
    """
    Parenthesized Go durations in the log tail, excluding Prow entrypoint JSON lines
    (which often end the file with ``10m0s`` grace, etc.).
    """
    if not text:
        return None
    tail = text[-400000:] if len(text) > 400000 else text
    best: Optional[int] = None
    for line in tail.splitlines():
        if _line_is_prow_entrypoint_noise(line):
            continue
        for m in re.finditer(r'\(\s*([^)]+)\s*\)', line):
            g = m.group(1).strip()
            if not re.search(r'\d', g):
                continue
            if not re.search(r'\d+\s*[hms]', g, re.I):
                continue
            secs = go_duration_to_seconds(g)
            if secs is not None and secs > 0:
                if best is None or secs > best:
                    best = secs
    return best


def _step_started_timestamp(base_url: str, variant: str, step_name: str) -> Optional[int]:
    """Epoch seconds from ``artifacts/{variant}/{step}/started.json``."""
    path = f"artifacts/{variant}/{step_name}/started.json"
    data = fetch_json_artifact(base_url, path)
    return _podutils_json_timestamp(data)


def _duration_openshift_extended_test_via_next_step(
    base_url: str, variant: str,
) -> Optional[int]:
    """
    Duration = start time of the next pipeline step minus openshift-extended-test start.

    Steps are ordered by ``started.json`` timestamp (sequential ci-operator steps). The
    step immediately after openshift-extended-test in that order is used when this step's
    ``finished.json`` is missing or unreliable (e.g. timeout).
    """
    step_dirs = get_step_directories(base_url, variant)
    if not step_dirs or 'openshift-extended-test' not in step_dirs:
        return None

    entries: List[Tuple[int, str]] = []
    for step in step_dirs:
        st = _step_started_timestamp(base_url, variant, step)
        if st is not None:
            entries.append((st, step))

    if len(entries) < 2:
        return None

    entries.sort(key=lambda x: (x[0], x[1]))

    for i in range(len(entries) - 1):
        if entries[i][1] == 'openshift-extended-test':
            t_oet = entries[i][0]
            t_next = entries[i + 1][0]
            if t_next > t_oet:
                return t_next - t_oet
            return None

    return None


def _build_log_shows_test_step_finished(text: Optional[str]) -> bool:
    """True if build-log contains the final openshift-tests error summary line."""
    if not text:
        return False
    return bool(
        re.search(
            r'(?i)error:\s*\d+\s*fail,\s*\d+\s*pass,\s*\d+\s*skip',
            text,
        )
    )


def _oet_step_has_finished(
    ts: Optional[int],
    tf: Optional[int],
    finished_json: Optional[Dict],
    build_log_text: Optional[str],
) -> bool:
    """
    Step is done if Prow wrote a finished record, we have an end timestamp, or build-log
    has the final test summary (tests completed / failed, not mid-run).
    """
    if finished_json is not None:
        if _podutils_json_timestamp(finished_json) is not None:
            return True
        if str(finished_json.get('result', '')).strip() != '':
            return True
        if 'passed' in finished_json:
            return True
    if ts is not None and tf is not None and tf >= ts:
        return True
    return _build_log_shows_test_step_finished(build_log_text)


def _parse_build_log_duration_seconds(build_log_text: Optional[str]) -> Optional[int]:
    """
    Best-effort seconds from build-log: the final ``error: N fail, … (duration)`` line from
    openshift-tests is treated as the suite elapsed time when present; otherwise longest
    ginkgo ``failed: (…)``, then other parenthesized durations (excluding Prow entrypoint
    noise).
    """
    if not build_log_text:
        return None
    suite = parse_test_step_duration_from_build_log(build_log_text)
    if suite is not None and suite > 0:
        return suite
    candidates: List[int] = []
    b = parse_ginkgo_failed_durations_max(build_log_text)
    if b is not None and b > 0:
        candidates.append(b)
    c = parse_build_log_duration_fallback(build_log_text)
    if c is not None and c > 0:
        candidates.append(c)
    return max(candidates) if candidates else None


def _merge_wall_and_log_seconds(
    sec_wall: Optional[int],
    sec_log: Optional[int],
) -> int:
    """
    Prefer wall-clock when it is at least one minute; otherwise prefer build-log.
    If both are positive, use the larger (covers sub-minute wall vs full suite in log).
    """
    w = sec_wall if sec_wall is not None else 0
    l = sec_log if sec_log is not None else 0
    if w >= 60:
        return max(w, l) if l > 0 else w
    if l > 0:
        return max(w, l)
    return w


def _oet_step_ended(
    ts: Optional[int],
    tf: Optional[int],
    fin_json: Optional[Dict],
    build_log_text: Optional[str],
    sec_next: Optional[int],
) -> bool:
    """
    True if the step is no longer running: Prow finished, test summary in log, or the
    next ci-operator step has started (interrupt/timeout without a clean finished.json).
    """
    if _oet_step_has_finished(ts, tf, fin_json, build_log_text):
        return True
    if ts is not None and sec_next is not None and sec_next > 0:
        return True
    return False


def compute_openshift_extended_test_elapsed_display(
    base_url: str,
    variant: str,
    extended_finished_json: Optional[Dict] = None,
) -> str:
    """
    How long the openshift-extended-test step ran, as ``HH:MM``.

    ``00:00`` only when the step never started or is still running (no finish artifact/log).

    When the step has finished, wall duration is ``finished`` minus ``started`` from
    ``finished.json`` / ``started.json`` when that delta is positive; otherwise **next
    step's ``started.json`` minus this step's start**. Merged with ``build-log.txt``
    when wall-clock is missing or sub-minute.
    """
    if not variant or variant == 'unknown':
        return ''

    from .parser import format_hours_minutes_test_step_display

    prefix = f"artifacts/{variant}/openshift-extended-test"
    started = fetch_json_artifact(base_url, f"{prefix}/started.json")
    ts = _podutils_json_timestamp(started)

    fin_json = extended_finished_json
    if fin_json is None:
        fin_json = fetch_json_artifact(base_url, f"{prefix}/finished.json")

    tf = _podutils_json_timestamp(fin_json)

    bl = fetch_artifact(base_url, f"{prefix}/build-log.txt")
    build_log_text: Optional[str] = None
    if bl:
        try:
            build_log_text = bl.decode('utf-8', errors='ignore')
        except Exception as e:
            logger.debug(f"Could not decode openshift-extended-test build-log.txt: {e}")

    sec_next: Optional[int] = None
    if ts is not None:
        sec_next = _duration_openshift_extended_test_via_next_step(base_url, variant)

    ended = _oet_step_ended(ts, tf, fin_json, build_log_text, sec_next)
    finished_prow = _oet_step_has_finished(ts, tf, fin_json, build_log_text)
    not_started = ts is None and not finished_prow
    running = ts is not None and not ended

    sec_log = _parse_build_log_duration_seconds(build_log_text)

    if not_started or running:
        return '00:00'

    # Ended: prefer positive finished.json wall delta; else next-step start − OET start.
    sec_wall: Optional[int] = None
    if ts is not None and tf is not None and tf >= ts:
        d = tf - ts
        if d > 0:
            sec_wall = d
    if sec_wall is None and sec_next is not None and sec_next > 0:
        sec_wall = sec_next

    secs = _merge_wall_and_log_seconds(sec_wall, sec_log)

    if secs <= 0:
        return 'unknown'

    return format_hours_minutes_test_step_display(secs)
