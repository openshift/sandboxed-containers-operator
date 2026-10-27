"""
Artifact Fetcher Module

Handles URL parsing and fetching artifacts from Prow/GCS.
Supports in-memory caching, a local directory tree, or a .tar.gz of the same layout.
"""

import json
import logging
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from typing import Dict, Optional, Tuple, List
from urllib.parse import quote
from urllib.request import urlopen, Request
from urllib.error import HTTPError, URLError

logger = logging.getLogger(__name__)

# Bytes fetched over HTTP or read from disk (keyed by root + relative path).
_artifact_cache: Dict[str, Optional[bytes]] = {}
# Resolved directory containing prowjob.json (and artifacts/, …); None = fetch over HTTP only.
_local_artifacts_root: Optional[str] = None
# Temp directory created when configuring from a .tar.gz (removed on next configure or process exit).
_tar_extract_temp_dir: Optional[str] = None


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


# The sandboxed-containers-operator-testsuites chain (replaces the retired
# OTP openshift-extended-test step; see the prowjob-analyzer plan). ``gate``
# is a test:-phase stub step that only touches a marker file; the three
# suites run in post:-phase, independently, with best_effort: true (a
# failing suite doesn't block the others). Confirmed live against real
# builds: all four exist as sibling artifact directories under
# ``artifacts/<variant>/``, and the top-level job result is FAILURE whenever
# any suite fails even though the others passed independently.
TESTSUITES_GATE_STEP = 'sandboxed-containers-operator-testsuites-gate'
TESTSUITES_SUITE_STEPS = {
    'osc': 'sandboxed-containers-operator-testsuites-osc',
    'kata-upstream': 'sandboxed-containers-operator-testsuites-kata-upstream',
    'caa': 'sandboxed-containers-operator-testsuites-caa',
}
ALL_TESTSUITES_STEPS = [TESTSUITES_GATE_STEP] + list(TESTSUITES_SUITE_STEPS.values())


_STORAGE_BASE = "https://storage.googleapis.com"
_STORAGE_API_BASE = "https://storage.googleapis.com/storage/v1/b"
_PROW_VIEW_GS_PREFIX = "https://prow.ci.openshift.org/view/gs/"

# Buckets Prow writes job artifacts to that now deny anonymous GCS reads
# (confirmed live: a direct anonymous request to test-platform-results
# returns "403 AccessDenied: Anonymous caller does not have
# storage.objects.get access", and even an authenticated-but-ungranted
# Red Hat account gets the same denial by name -- this is an IAM grant
# restricted to specific identities, not a login problem). OpenShift CI
# publishes a "-public" mirror bucket with the same object layout and
# anonymous read access for exactly this kind of tooling; map to it here.
# The old approach of routing through the gcsweb-ci web UI
# (gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com) no longer works either: that
# host now redirects to gcs.ci.openshift.org, which 404s for this bucket
# while serving the "-public" bucket fine -- i.e. the proxy itself has been
# reconfigured to only route the public mirror, so there is no reason to
# depend on it at all; fetch the public mirror directly.
_PUBLIC_BUCKET_MIRRORS = {
    'test-platform-results': 'test-platform-results-public',
}


def _public_bucket_name(bucket: str) -> str:
    """Map a Prow GCS bucket to its anonymously-readable public mirror, if any."""
    return _PUBLIC_BUCKET_MIRRORS.get(bucket, bucket)


def prow_url_to_public_storage_base(prow_url: str) -> Optional[str]:
    """
    Convert a Prow ``/view/gs/BUCKET/path`` URL into the equivalent, directly
    fetchable ``storage.googleapis.com`` URL on the anonymous-read public
    mirror bucket.

    This is a pure string transform (no HTTP request): Prow's Deck UI is a
    client-rendered SPA, so fetching a ``/view/gs/...`` URL over plain HTTP
    returns an HTML shell rather than artifact bytes, and there is nothing to
    discover by scraping it. See the module-level comment on
    ``_PUBLIC_BUCKET_MIRRORS`` for why the bucket name itself is remapped.

    Returns ``None`` if ``prow_url`` doesn't match the expected
    ``https://prow.ci.openshift.org/view/gs/...`` shape.
    """
    if not prow_url.startswith(_PROW_VIEW_GS_PREFIX):
        return None
    rest = prow_url[len(_PROW_VIEW_GS_PREFIX):]
    if '/' not in rest:
        return None
    bucket, path = rest.split('/', 1)
    if not bucket or not path:
        return None
    return f"{_STORAGE_BASE}/{_public_bucket_name(bucket)}/{path}"


def _split_storage_url(storage_url: str) -> Tuple[Optional[str], Optional[str]]:
    """Split a ``https://storage.googleapis.com/BUCKET/path`` URL into ``(bucket, path)``."""
    prefix = f"{_STORAGE_BASE}/"
    if not storage_url.lower().startswith(prefix.lower()):
        return None, None
    rest = storage_url[len(prefix):].strip('/')
    if not rest:
        return None, None
    if '/' not in rest:
        return rest, ''
    bucket, path = rest.split('/', 1)
    return bucket, path


def _resolve_fetch_base_url(base_url: str) -> str:
    """
    Resolve the URL to actually fetch artifact bytes/listings from.

    ``base_url`` is normally a Prow UI URL
    (``https://prow.ci.openshift.org/view/gs/...``); convert it once to the
    equivalent public-mirror ``storage.googleapis.com`` URL (see
    :func:`prow_url_to_public_storage_base`). If it doesn't match that shape
    (e.g. a direct storage URL passed in tests), use it unchanged.
    """
    public = prow_url_to_public_storage_base(base_url)
    return public or base_url


def gcs_uri_from_public_storage_https(https_url: str) -> Optional[str]:
    """
    Convert a ``https://storage.googleapis.com/BUCKET/path`` URL to ``gs://BUCKET/path``.

    If ``https_url`` is already ``gs://``, return it unchanged.
    """
    u = https_url.strip()
    if u.lower().startswith('gs://'):
        return u.rstrip('/')
    bucket, path = _split_storage_url(u.split('?')[0].split('#')[0])
    if not bucket:
        return None
    return f'gs://{bucket}/{path}' if path else f'gs://{bucket}'


def gcs_uri_for_job_artifacts(prow_job_base_url: str) -> str:
    """
    Resolve the ``gs://`` prefix for the job artifact tree (same object prefix as on
    the Prow Artifacts page / ``gsutil`` examples), on the anonymously-readable
    public mirror bucket (see :func:`prow_url_to_public_storage_base`).
    """
    u = prow_job_base_url.strip().rstrip('/')
    if u.lower().startswith('gs://'):
        return u
    https = u
    if not u.lower().startswith(_STORAGE_BASE.lower()):
        public = prow_url_to_public_storage_base(u)
        if not public:
            raise ValueError(
                'Could not derive a storage.googleapis.com URL from this Prow page. '
                'Expected a https://prow.ci.openshift.org/view/gs/... URL.'
            )
        https = public
    uri = gcs_uri_from_public_storage_https(https)
    if not uri:
        raise ValueError(
            'Could not convert the artifact URL to a gs:// URI.'
        )
    return uri


def download_job_artifacts(prow_job_base_url: str, parent_directory: str, build_id: str) -> str:
    """
    Download the job artifact tree into ``parent_directory/build_id/`` using ``gsutil``.

    Runs the same style of command as on the Prow Artifacts page: ``gsutil -m cp -r``
    from the job's ``gs://`` prefix into the destination directory. Requires ``gsutil``
    on ``PATH`` (Google Cloud SDK).

    Args:
        prow_job_base_url: Prow job base URL from :func:`parse_prow_url`
        parent_directory: Directory in which to create the ``build_id`` folder
        build_id: Last path segment of the job URL (folder name)

    Returns:
        Absolute path to ``parent_directory/build_id``
    """
    gcs_uri = gcs_uri_for_job_artifacts(prow_job_base_url)

    dest = os.path.abspath(os.path.join(os.path.expanduser(parent_directory), build_id))
    if os.path.exists(dest) and not os.path.isdir(dest):
        raise FileExistsError(dest)
    os.makedirs(dest, exist_ok=True)

    src = gcs_uri.rstrip('/') + '/'
    logger.info("Downloading artifacts with gsutil ( %s -> %s )", src, dest)
    try:
        subprocess.run(
            ['gsutil', '-m', 'cp', '-r', src, dest],
            check=True,
        )
    except FileNotFoundError as e:
        raise FileNotFoundError(
            'gsutil not found; install Google Cloud SDK so gsutil is on PATH, '
            'or download artifacts manually using the command from the Prow Artifacts page.'
        ) from e
    except subprocess.CalledProcessError as e:
        raise RuntimeError(
            f'gsutil failed with exit code {e.returncode}'
        ) from e

    return dest


def write_job_artifacts_tarball(artifact_dir: str, tar_gz_path: str) -> None:
    """
    Write a gzip-compressed tar of ``artifact_dir`` (top-level folder name preserved).

    Args:
        artifact_dir: Path to the job artifacts directory (e.g. ``.../build_id``)
        tar_gz_path: Output file path (e.g. ``.../build_id.tar.gz``)
    """
    artifact_dir = os.path.abspath(artifact_dir)
    if not os.path.isdir(artifact_dir):
        raise NotADirectoryError(artifact_dir)
    base = os.path.basename(artifact_dir.rstrip(os.sep))
    tar_abs = os.path.abspath(tar_gz_path)
    tar_parent = os.path.dirname(tar_abs)
    if tar_parent:
        os.makedirs(tar_parent, exist_ok=True)
    logger.info("Writing tarball %s from %s", tar_abs, artifact_dir)
    with tarfile.open(tar_abs, 'w:gz') as tf:
        tf.add(artifact_dir, arcname=base)


def clear_artifact_cache() -> None:
    """Clear the in-memory artifact cache (e.g. before switching artifact source)."""
    _artifact_cache.clear()


def is_local_artifact_mode() -> bool:
    """True when reads come from a configured local directory or extracted archive."""
    return _local_artifacts_root is not None


def _find_job_root(path: str) -> str:
    """
    If ``prowjob.json`` is not at ``path``, use the first subdirectory that contains it
    (common for tar layouts with a single top-level folder).
    """
    if os.path.isfile(os.path.join(path, 'prowjob.json')):
        return path
    try:
        names = sorted(os.listdir(path))
    except OSError:
        return path
    for name in names:
        sub = os.path.join(path, name)
        if os.path.isdir(sub) and os.path.isfile(os.path.join(sub, 'prowjob.json')):
            return sub
    return path


def configure_artifact_source(path: Optional[str]) -> None:
    """
    Use a local directory or ``.tar.gz`` of job artifacts instead of (or before) HTTP.

    Layout matches Prow: ``prowjob.json``, ``finished.json``, ``artifacts/<variant>/…``.

    Clears the artifact cache. Call with ``None`` to use only network fetches again.
    Removes a previously extracted tar temp directory when reconfiguring.
    """
    global _local_artifacts_root, _tar_extract_temp_dir

    clear_artifact_cache()

    if _tar_extract_temp_dir and os.path.isdir(_tar_extract_temp_dir):
        shutil.rmtree(_tar_extract_temp_dir, ignore_errors=True)
    _tar_extract_temp_dir = None
    _local_artifacts_root = None

    if not path:
        return

    path = os.path.abspath(os.path.expanduser(path.strip()))
    if not os.path.exists(path):
        raise FileNotFoundError(path)

    if path.lower().endswith(('.tar.gz', '.tgz')):
        if not os.path.isfile(path):
            raise ValueError(f"Not a file: {path}")
        td = tempfile.mkdtemp(prefix='prowjob-analyzer-artifacts-')
        _tar_extract_temp_dir = td
        logger.info(f"Extracting artifact archive to {td}...")
        with tarfile.open(path, 'r:*') as tf:
            if sys.version_info >= (3, 12):
                tf.extractall(td, filter='data')
            else:
                tf.extractall(td)
        _local_artifacts_root = _find_job_root(td)
    elif os.path.isdir(path):
        _local_artifacts_root = _find_job_root(path)
    else:
        raise ValueError(f"Expected a directory or .tar.gz, got: {path}")

    if not os.path.isfile(os.path.join(_local_artifacts_root, 'prowjob.json')):
        logger.warning(
            f"No prowjob.json under artifact root {_local_artifacts_root!r}; "
            "analysis may fail."
        )
    logger.info(f"Artifact source: local root {_local_artifacts_root}")


def _artifact_cache_key(base_url: str, artifact_path: str) -> str:
    root = _local_artifacts_root or base_url
    return f"{root}\0{artifact_path}"


def _read_local_artifact(artifact_path: str) -> Optional[bytes]:
    if not _local_artifacts_root:
        return None
    full = os.path.join(_local_artifacts_root, artifact_path.replace('/', os.sep))
    if not os.path.isfile(full):
        return None
    with open(full, 'rb') as f:
        return f.read()


def _fetch_artifact_http(base_url: str, artifact_path: str, max_retries: int = 3) -> Optional[bytes]:
    """HTTP(S) fetch only (no cache)."""
    url = f"{_resolve_fetch_base_url(base_url)}/{artifact_path}"

    for attempt in range(max_retries):
        try:
            logger.debug(f"Fetching {url} (attempt {attempt + 1}/{max_retries})")
            req = Request(url, headers={'User-Agent': 'prowjob-analyzer/1.0'})

            with urlopen(req, timeout=30) as response:
                content_type = response.headers.get('Content-Type', '')
                content = response.read()

                # A stray HTML response means we ended up at a URL that isn't
                # the raw artifact (e.g. base_url wasn't a recognized Prow/
                # storage shape) -- treat as not found rather than returning
                # a page of markup as if it were the artifact.
                if 'text/html' in content_type:
                    logger.debug(f"Got HTML response - artifact not found: {url}")
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


def fetch_artifact(base_url: str, artifact_path: str, max_retries: int = 3) -> Optional[bytes]:
    """
    Read an artifact from the configured local tree, or fetch from Prow/GCS over HTTP.

    When :func:`configure_artifact_source` has set a local directory or extracted
    ``.tar.gz``, **only disk reads** are used for job artifacts—``base_url`` is ignored
    for retrieval (still used as a cache key). Otherwise HTTP is used with retries.

    Successful reads are cached in-memory for the lifetime of the process (including
    ``None`` for missing files) to avoid duplicate network or disk reads.

    Args:
        base_url: Base URL of the Prow job (cache key; used for HTTP when not local)
        artifact_path: Relative path to the artifact (e.g., "prowjob.json")
        max_retries: Maximum number of retry attempts (HTTP only)

    Returns:
        Artifact content as bytes, or None if not found
    """
    key = _artifact_cache_key(base_url, artifact_path)
    if key in _artifact_cache:
        return _artifact_cache[key]

    if _local_artifacts_root:
        data = _read_local_artifact(artifact_path)
        _artifact_cache[key] = data
        if data is not None:
            logger.debug(f"Read local artifact {artifact_path} ({len(data)} bytes)")
        else:
            logger.debug(f"Local artifact not found: {artifact_path}")
        return data

    data = _fetch_artifact_http(base_url, artifact_path, max_retries=max_retries)
    _artifact_cache[key] = data
    return data


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
    Wait for job artifacts to become available on the remote job (HTTP polling).

    When a local artifact tree is configured, does not poll the network: checks once
    for ``finished.json`` under the local root.

    Args:
        base_url: Base URL of the Prow job
        timeout: Maximum time to wait in seconds (default: 5 minutes)
        poll_interval: Time between polls in seconds (default: 10 seconds)

    Returns:
        True if artifacts became ready, False if timeout
    """
    if _local_artifacts_root:
        if check_artifacts_ready(base_url):
            logger.info("Local artifacts present (finished.json found).")
            return True
        logger.warning("Local artifact root is missing finished.json.")
        return False

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
    Generate a GCS web URL for an artifact, or a ``file://`` URI when using local artifacts.

    Args:
        base_url: Base URL of the Prow job
        artifact_path: Relative path to the artifact

    Returns:
        Full URL to the artifact
    """
    if _local_artifacts_root:
        full = os.path.join(_local_artifacts_root, artifact_path.replace('/', os.sep))
        if os.path.isfile(full):
            return Path(full).resolve().as_uri()
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
    if _local_artifacts_root:
        base = os.path.join(_local_artifacts_root, 'artifacts', variant)
        if not os.path.isdir(base):
            logger.debug(f"No local artifacts/{variant}/ directory: {base}")
            return []
        step_dirs: List[str] = []
        try:
            for name in sorted(os.listdir(base)):
                if name in ('.', '..', 'artifacts'):
                    continue
                p = os.path.join(base, name)
                if os.path.isdir(p):
                    step_dirs.append(name)
        except OSError as e:
            logger.warning(f"Failed to list local step directories {base}: {e}")
            return []
        logger.debug(f"Found {len(step_dirs)} local step directories in {variant}")
        return step_dirs

    storage_base = _resolve_fetch_base_url(base_url)
    bucket, root_path = _split_storage_url(storage_base)
    if not bucket:
        logger.warning(
            f"Could not resolve a storage.googleapis.com URL for step listing: {base_url}"
        )
        return []

    list_prefix = f"{root_path}/artifacts/{variant}/"
    list_url = (
        f"{_STORAGE_API_BASE}/{bucket}/o"
        f"?prefix={quote(list_prefix, safe='/')}&delimiter=/"
    )

    try:
        logger.debug(f"Listing step directories via GCS API: {list_url}")
        req = Request(list_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
        with urlopen(req, timeout=30) as response:
            data = json.loads(response.read().decode('utf-8'))

        # The GCS JSON list API returns subdirectory-like prefixes directly
        # when delimiter=/ is set -- no HTML to scrape.
        step_dirs = []
        for prefix in data.get('prefixes', []):
            if not prefix.startswith(list_prefix):
                continue
            name = prefix[len(list_prefix):].rstrip('/')
            if name and name not in ('.', '..', 'artifacts'):
                step_dirs.append(name)

        logger.debug(f"Found {len(step_dirs)} step directories in {variant}: {step_dirs[:5]}")
        return sorted(step_dirs)

    except Exception as e:
        logger.warning(f"Failed to get step directories from {list_url}: {e}")
        return []


def list_artifact_files(base_url: str, dir_path: str, name_prefix: str = '') -> List[str]:
    """
    List artifact file names (not subdirectories) directly under ``dir_path``
    (relative to the job root), optionally filtered to names starting with
    ``name_prefix``.

    Used for suites that write a variable number of JUnit files per run (e.g.
    ``sandboxed-containers-operator-testsuites-kata-upstream`` writes one
    ``junit_kata_upstream_<bats-file>.xml`` per bats suite, and
    ``...-testsuites-caa`` writes one or more ``junit_caa_*.xml``) --
    directory listing is unavoidable there, unlike the single, fixed
    ``junit_osc.xml``/``junit_osc_skip.xml`` names.
    """
    if _local_artifacts_root:
        base = os.path.join(_local_artifacts_root, dir_path.replace('/', os.sep))
        if not os.path.isdir(base):
            return []
        try:
            return sorted(
                n for n in os.listdir(base)
                if n.startswith(name_prefix) and os.path.isfile(os.path.join(base, n))
            )
        except OSError as e:
            logger.warning(f"Failed to list local artifact files {base}: {e}")
            return []

    storage_base = _resolve_fetch_base_url(base_url)
    bucket, root_path = _split_storage_url(storage_base)
    if not bucket:
        logger.warning(
            f"Could not resolve a storage.googleapis.com URL for file listing: {base_url}"
        )
        return []

    list_prefix = f"{root_path}/{dir_path}/".replace('//', '/')
    list_url = (
        f"{_STORAGE_API_BASE}/{bucket}/o"
        f"?prefix={quote(list_prefix, safe='/')}&delimiter=/"
    )

    try:
        logger.debug(f"Listing artifact files via GCS API: {list_url}")
        req = Request(list_url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
        with urlopen(req, timeout=30) as response:
            data = json.loads(response.read().decode('utf-8'))

        names = []
        for item in data.get('items', []):
            full_name = item.get('name', '')
            if not full_name.startswith(list_prefix):
                continue
            rel = full_name[len(list_prefix):]
            if not rel or '/' in rel:
                continue  # nested or empty, not a direct child file
            if rel.startswith(name_prefix):
                names.append(rel)
        return sorted(names)

    except Exception as e:
        logger.warning(f"Failed to list artifact files from {list_url}: {e}")
        return []


def summarize_test_step_build_log(log_text: str) -> Optional[str]:
    """
    Build a short label for a test step from its build-log.txt. Generic --
    not specific to any one step (originally written for the retired OTP
    openshift-extended-test step; kept for any step whose runner emits the
    same ``openshift-tests``-style summary line).

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


def get_failed_steps(base_url: str, variant: str) -> List[str]:
    """
    Identify which steps failed by checking each one's own finished.json.

    Each step's finished.json is authoritative on its own in the current
    (testsuites-chain) architecture. The retired OTP openshift-extended-test
    step needed a test-results.yaml cross-check because it could report
    "passed" even when its own tests failed; no step here is known to have
    that mismatch, so no cross-check is needed.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant (e.g., "aws-ipi-peerpods")

    Returns:
        List of failed step directory names.
    """
    step_dirs = get_step_directories(base_url, variant)
    failed_steps = []

    for step in step_dirs:
        finished_path = f"artifacts/{variant}/{step}/finished.json"
        finished_data = fetch_json_artifact(base_url, finished_path)

        if finished_data:
            passed = finished_data.get('passed', True)
            result = (finished_data.get('result') or '').upper()
            step_failed = not passed or result == 'FAILURE'

            if step_failed:
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


def _duration_step_via_next_step(
    base_url: str, variant: str, step_name: str,
) -> Optional[int]:
    """
    Duration = start time of the next pipeline step minus ``step_name``'s start.

    Steps are ordered by ``started.json`` timestamp (sequential ci-operator steps). The
    step immediately after ``step_name`` in that order is used when this step's
    ``finished.json`` is missing or unreliable (e.g. timeout).
    """
    step_dirs = get_step_directories(base_url, variant)
    if not step_dirs or step_name not in step_dirs:
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
        if entries[i][1] == step_name:
            t_this = entries[i][0]
            t_next = entries[i + 1][0]
            if t_next > t_this:
                return t_next - t_this
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


def _step_has_finished(
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


def _step_ended(
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
    if _step_has_finished(ts, tf, fin_json, build_log_text):
        return True
    if ts is not None and sec_next is not None and sec_next > 0:
        return True
    return False


def compute_step_elapsed_display(
    base_url: str,
    variant: str,
    step_name: str,
    step_finished_json: Optional[Dict] = None,
) -> str:
    """
    How long a given step ran, as ``HH:MM``.

    Generalized from the retired OTP openshift-extended-test step's elapsed-time
    computation -- now takes the step name as a parameter instead of hardcoding
    it, so it works for any ``sandboxed-containers-operator-testsuites-*`` step
    (or any other step).

    ``00:00`` only when the step never started or is still running (no finish artifact/log).

    When the step has finished, wall duration is ``finished`` minus ``started`` from
    ``finished.json`` / ``started.json`` when that delta is positive; otherwise **next
    step's ``started.json`` minus this step's start**. Merged with ``build-log.txt``
    when wall-clock is missing or sub-minute.
    """
    if not variant or variant == 'unknown':
        return ''

    from .parser import format_hours_minutes_test_step_display

    prefix = f"artifacts/{variant}/{step_name}"
    started = fetch_json_artifact(base_url, f"{prefix}/started.json")
    ts = _podutils_json_timestamp(started)

    fin_json = step_finished_json
    if fin_json is None:
        fin_json = fetch_json_artifact(base_url, f"{prefix}/finished.json")

    tf = _podutils_json_timestamp(fin_json)

    bl = fetch_artifact(base_url, f"{prefix}/build-log.txt")
    build_log_text: Optional[str] = None
    if bl:
        try:
            build_log_text = bl.decode('utf-8', errors='ignore')
        except Exception as e:
            logger.debug(f"Could not decode {step_name} build-log.txt: {e}")

    sec_next: Optional[int] = None
    if ts is not None:
        sec_next = _duration_step_via_next_step(base_url, variant, step_name)

    ended = _step_ended(ts, tf, fin_json, build_log_text, sec_next)
    finished_prow = _step_has_finished(ts, tf, fin_json, build_log_text)
    not_started = ts is None and not finished_prow
    running = ts is not None and not ended

    sec_log = _parse_build_log_duration_seconds(build_log_text)

    if not_started or running:
        return '00:00'

    # Ended: prefer positive finished.json wall delta; else next-step start − this step's start.
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


def compute_testsuites_phase_elapsed_display(base_url: str, variant: str) -> str:
    """
    Wall-clock time spent in the sandboxed-containers-operator-testsuites chain
    (the test:-phase gate step plus the post:-phase osc/kata-upstream/caa
    suites), replacing the retired single openshift-extended-test step's
    elapsed-time metric.

    None of these steps write a ``started.json`` (confirmed live -- every
    one of the four returns 404 for it, even when ``finished.json`` exists),
    so unlike :func:`compute_step_elapsed_display` this can't use a
    started/finished wall-clock delta. Instead, sums each step's own
    build-log.txt-derived duration (see :func:`_parse_build_log_duration_seconds`)
    -- steps in a ``chain:`` run sequentially, so the sum approximates total
    chain wall time. Returns ``'unknown'`` when no step yields any duration
    signal (e.g. the job hasn't reached this chain yet).
    """
    if not variant or variant == 'unknown':
        return ''

    from .parser import format_hours_minutes_test_step_display

    total_secs = 0
    have_signal = False

    for step_name in ALL_TESTSUITES_STEPS:
        bl = fetch_artifact(base_url, f"artifacts/{variant}/{step_name}/build-log.txt")
        if not bl:
            continue
        try:
            text = bl.decode('utf-8', errors='ignore')
        except Exception as e:
            logger.debug(f"Could not decode {step_name} build-log.txt: {e}")
            continue
        secs = _parse_build_log_duration_seconds(text)
        if secs and secs > 0:
            total_secs += secs
            have_signal = True

    if not have_signal or total_secs <= 0:
        return 'unknown'

    return format_hours_minutes_test_step_display(total_secs)
