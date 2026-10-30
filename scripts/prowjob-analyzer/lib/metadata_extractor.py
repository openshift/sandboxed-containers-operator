"""
Metadata Extractor Module

Extracts job metadata from prowjob.json and related artifacts.
"""

import csv
import io
import re
import json
import logging
import urllib.request
import urllib.error
from datetime import datetime, timezone
from typing import Dict, Optional
from .fetcher import fetch_artifact, extract_variant_from_job_name

logger = logging.getLogger(__name__)

# ``sandboxed-containers-operator-pre`` chain order (see the `release` repo's
# sandboxed-containers-operator-pre-chain.yaml): install-osc-operator (resolves
# a ``:latest`` CATALOG_SOURCE_IMAGE tag to the real build tag) runs *before*
# update-kata-rpm (installs the RPM, if requested), which runs *before*
# record-metadata (writes a single CSV with the already-resolved catalog tag,
# a Quay-queried build time, and the RPM version). record-metadata is the
# authoritative, purpose-built source for both fields this module used to
# struggle with (KATA-6339); the older per-field extractors below are kept
# only as a fallback for jobs that predate this step.
RECORD_METADATA_STEP = 'sandboxed-containers-operator-record-metadata'
RECORD_METADATA_CSV_PATH = 'test-metadata.csv'
# NOT "sandboxed-containers-operator-get-kata-rpm" -- that step name never
# existed in the `release` step-registry; this is the real one.
UPDATE_KATA_RPM_STEP = 'sandboxed-containers-operator-update-kata-rpm'


def extract_provider(job_name: str) -> str:
    """
    Extract cloud provider from job name.

    Args:
        job_name: Prow job name

    Returns:
        Provider name (aws, azure, gcp, etc.) or 'unknown'
    """
    providers = {
        'aro': 'Aro',
        'aws': 'AWS',
        'azure': 'Azure',
        'gcp': 'GCP',
        'ibmcloud': 'IBM Cloud',
        'vsphere': 'vSphere',
        'openstack': 'OpenStack',
        'baremetal': 'Bare Metal',
    }

    job_lower = job_name.lower()
    for key, value in providers.items():
        if key in job_lower:
            return value

    return 'unknown'


def extract_workload_type(job_name: str) -> str:
    """
    Extract workload type from job name.

    Args:
        job_name: Prow job name

    Returns:
        Workload type (kata, peerpods, coco, etc.) or 'unknown'
    """
    job_lower = job_name.lower()

    if 'peerpods' in job_lower or 'peer-pods' in job_lower:
        return 'peerpods'
    elif 'coco' in job_lower or 'confidential' in job_lower:
        return 'confidential-containers'
    elif 'kata' in job_lower:
        return 'kata'
    else:
        return 'unknown'


def extract_ocp_version_from_release_artifact(base_url: str) -> Optional[str]:
    """
    Extract exact OCP version from release-images-latest artifact.

    Args:
        base_url: Base URL of the Prow job

    Returns:
        OCP version string (e.g., "4.17.45") or None if not found
    """
    release_artifact_path = "artifacts/release/artifacts/release-images-latest"
    content = fetch_artifact(base_url, release_artifact_path)

    if content:
        try:
            # Check if we got HTML (directory listing) instead of JSON
            content_str = content.decode('utf-8', errors='ignore')
            if content_str.strip().startswith('<!doctype') or content_str.strip().startswith('<html'):
                logger.debug("Got HTML instead of release-images-latest, file doesn't exist")
                return None

            # Parse JSON ImageStream
            data = json.loads(content_str)

            # Extract version from metadata.name
            if 'metadata' in data and 'name' in data['metadata']:
                version = data['metadata']['name']
                logger.debug(f"Found OCP version from release artifact: {version}")
                return version

        except json.JSONDecodeError as e:
            logger.debug(f"Failed to parse release-images-latest as JSON: {e}")
        except Exception as e:
            logger.debug(f"Failed to extract OCP version from release artifact: {e}")

    return None


def extract_ocp_version(prowjob_data: Dict, base_url: str = None) -> str:
    """
    Extract OCP version from release artifact or prowjob metadata.

    Args:
        prowjob_data: Parsed prowjob.json
        base_url: Optional base URL for fetching release artifact

    Returns:
        OCP version string (e.g., "4.17.45") or 'unknown'
    """
    # First try to get exact version from release artifact
    if base_url:
        version = extract_ocp_version_from_release_artifact(base_url)
        if version:
            return version
        logger.debug("Falling back to prowjob metadata for OCP version")

    # Try to extract from environment variables
    env_vars = prowjob_data.get('env_vars', {})

    # Check for common OCP version variables
    for var_name in ['OPENSHIFT_VERSION', 'OCP_VERSION', 'RELEASE_IMAGE_LATEST']:
        if var_name in env_vars:
            value = env_vars[var_name]
            # Try to extract version number (e.g., "4.19" from various formats)
            match = re.search(r'(\d+\.\d+)', value)
            if match:
                return match.group(1)

    # Try to extract from job name
    # Pattern: ...ocp-4.19-... or ...4-19-...
    job_name = prowjob_data.get('job_name', '')
    match = re.search(r'(?:ocp-)?(\d+)[-\.](\d+)', job_name)
    if match:
        return f"{match.group(1)}.{match.group(2)}"

    # Try labels
    labels = prowjob_data.get('metadata', {}).get('labels', {})
    if 'prow.k8s.io/release' in labels:
        return labels['prow.k8s.io/release']

    return 'unknown'


def fetch_build_date_from_quay(catalog_image: str) -> str:
    """
    For quay.io/...@sha256:..., call Quay API, find tag by manifest_digest,
    return parsed last_modified or a problem value string.

    Returns:
        ISO-style build date string, or one of:
        "quay-unavailable" (network/API error),
        "quay-not-found" (digest not in tag list),
        "unknown" (not a quay digest image or generic failure).
    """
    if not catalog_image or '@' not in catalog_image:
        return 'unknown'
    tag_part = catalog_image.split('@')[-1]
    if not tag_part.startswith('sha256:'):
        return 'unknown'
    # Must be quay.io for this implementation
    if not catalog_image.startswith('quay.io/'):
        return 'unknown'
    # Parse: quay.io/namespace/repo@sha256:digest
    rest = catalog_image[len('quay.io/'):]
    if '/' not in rest:
        return 'unknown'
    repo = rest.split('@')[0]  # namespace/repo
    api_url = f"https://quay.io/api/v1/repository/{repo}/tag/?limit=500"
    try:
        req = urllib.request.Request(api_url, headers={'Accept': 'application/json'})
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode('utf-8'))
    except (urllib.error.URLError, urllib.error.HTTPError, OSError, json.JSONDecodeError) as e:
        logger.debug(f"Quay API request failed for {repo}: {e}")
        return 'quay-unavailable'

    tags = data.get('tags') or []
    for t in tags:
        manifest_digest = t.get('manifest_digest') or ''
        if manifest_digest == tag_part or manifest_digest == tag_part[7:]:
            last_modified = t.get('last_modified')
            if last_modified:
                try:
                    # Quay returns e.g. "Mon, 01 Jan 2024 12:00:00 -0000" or ISO
                    if 'T' in last_modified:
                        dt = datetime.fromisoformat(last_modified.replace('Z', '+00:00'))
                    else:
                        from email.utils import parsedate_to_datetime
                        dt = parsedate_to_datetime(last_modified)
                    if dt.tzinfo is None:
                        dt = dt.replace(tzinfo=timezone.utc)
                    return dt.strftime('%Y-%m-%d %H:%M:%S UTC')
                except (ValueError, TypeError) as e:
                    logger.debug(f"Failed to parse last_modified {last_modified}: {e}")
                    return 'unknown'
            return 'unknown'
    return 'quay-not-found'


def _parse_tag_version_info(tag: str) -> Dict:
    """
    Parse a bare image tag (no registry/repo, no leading ``:``) into
    base_version/timestamp/catalog_build_date.

    Handles multiple tag formats:
    - version-timestamp: 1.11.1-1765791442
    - version with v prefix: v1.11.1
    - plain version: 1.11.1
    - latest: latest
    - git SHA: abc123def456

    Shared by ``parse_catalog_tag`` (prowjob.json env var path) and the
    record-metadata CSV path (``parse_record_metadata_csv``), since both end
    up with a bare tag string to classify the same way.

    Args:
        tag: Bare tag string (e.g. "1.11.1-1765791442", "latest")

    Returns:
        Dictionary with 'base_version', 'timestamp', 'catalog_build_date'
    """
    info = {'base_version': '', 'timestamp': '', 'catalog_build_date': ''}

    # sha256 digest tag (e.g. from record-metadata's CSV for a digest-pinned
    # CATALOG_SOURCE_IMAGE): strip the algorithm prefix, like parse_catalog_tag
    # does for the "@sha256:..." form. No build_date here -- the caller
    # supplies that separately (Quay digest lookup).
    if tag.startswith('sha256:'):
        info['base_version'] = tag[len('sha256:'):]
        info['catalog_build_date'] = 'unknown'
        return info

    # Try version-timestamp format (e.g., "1.11.1-1765791442")
    match = re.match(r'^([\d.]+)-(\d+)$', tag)
    if match:
        info['base_version'] = match.group(1)
        info['timestamp'] = match.group(2)

        # Convert timestamp to UTC datetime
        ts_raw = match.group(2)
        try:
            timestamp_int = int(ts_raw)
            dt = datetime.fromtimestamp(timestamp_int, tz=timezone.utc)
            info['catalog_build_date'] = dt.strftime('%Y-%m-%d %H:%M:%S UTC')
        except (ValueError, OSError) as e:
            logger.debug(f"Failed to convert timestamp {ts_raw}: {e}")
            info['catalog_build_date'] = f'invalid-timestamp-{ts_raw}'
        return info

    # Try version with v prefix (e.g., "v1.11.1")
    match = re.match(r'^v([\d.]+)$', tag)
    if match:
        info['base_version'] = match.group(1)
        info['catalog_build_date'] = 'unknown'
        return info

    # Try plain version (e.g., "1.11.1")
    match = re.match(r'^[\d.]+$', tag)
    if match:
        info['base_version'] = tag
        info['catalog_build_date'] = 'unknown'
        return info

    # Check for "latest"
    if tag == 'latest':
        info['base_version'] = 'latest'
        info['catalog_build_date'] = 'unknown'
        return info

    # Check for git SHA (40 hex characters for full SHA, or shorter for abbreviated)
    if re.match(r'^[0-9a-f]{7,40}$', tag):
        info['base_version'] = tag
        info['catalog_build_date'] = 'unknown'
        return info

    # Default: unknown format, keep as-is
    info['base_version'] = tag
    info['catalog_build_date'] = 'unknown'
    return info


def parse_catalog_tag(catalog_image: str) -> Dict:
    """
    Parse catalog image tag to extract version and timestamp.

    Handles multiple tag formats (see ``_parse_tag_version_info``) plus the
    sha256 digest form (build_date from Quay API when quay.io).

    Problem values for build_date: "not set", "invalid-timestamp", "quay-unavailable",
    "quay-not-found", "unknown".

    Note: for jobs with the ``sandboxed-containers-operator-record-metadata``
    step (see ``fetch_record_metadata``), ``catalog_image`` here is usually
    the *unresolved* value from prowjob.json's env vars (e.g. a literal
    ``...osc-test-fbc:latest``, since ci-operator configs set it statically) --
    not the real tag actually pulled at runtime, which install-osc-operator
    only resolves and records on the live cluster. ``extract_metadata``
    prefers the record-metadata CSV's already-resolved tag when available and
    only falls back to this function's output otherwise.

    Args:
        catalog_image: Full catalog image string (e.g., quay.io/.../osc-test-fbc:1.11.1-1765791442)

    Returns:
        Dictionary with parsed catalog information
    """
    catalog_info = {
        'full_tag': '',
        'base_version': '',
        'timestamp': '',
        'catalog_build_date': '',
    }

    if not catalog_image:
        catalog_info['catalog_build_date'] = 'not set'
        return catalog_info

    # Check if using digest format (with @)
    if '@' in catalog_image:
        # Format: quay.io/example@sha256:41ca9598b816ddc784de8f345e7e586c2630747f2d10c1a45ad5e082ff228a1f
        tag = catalog_image.split('@')[-1]
        catalog_info['full_tag'] = tag
        if ':' in tag:
            algorithm, hash_value = tag.split(':', 1)
            catalog_info['base_version'] = hash_value
        else:
            catalog_info['base_version'] = tag
        catalog_info['catalog_build_date'] = fetch_build_date_from_quay(catalog_image)
        return catalog_info

    # Check if using tag format (with :)
    if ':' not in catalog_image:
        return catalog_info

    # Extract tag from image
    tag = catalog_image.split(':')[-1]
    catalog_info['full_tag'] = tag
    catalog_info.update(_parse_tag_version_info(tag))
    return catalog_info


# CSV field values that mean "this wasn't actually captured", written by
# sandboxed-containers-operator-record-metadata-commands.sh itself.
_RECORD_METADATA_PROBLEM_VALUES = {
    '', 'image not set', 'cannot be found', 'cannot be found (no kubeconfig)', '<not set>',
}


def parse_record_metadata_csv(content: bytes) -> Optional[Dict[str, str]]:
    """
    Parse the ``test-metadata.csv`` artifact written by the
    sandboxed-containers-operator-record-metadata step.

    Expected columns (single data row): timestamp, catalog_tag, build_time,
    cloud_provider, ocp_version, prow_job_url, workload_to_test,
    kata_rpm_version.

    Args:
        content: Raw bytes of test-metadata.csv

    Returns:
        Dict of column -> value for the first data row, or None if the
        content is missing/HTML/empty/unparseable.
    """
    if not content:
        return None
    try:
        text = content.decode('utf-8', errors='ignore')
    except Exception as e:
        logger.debug(f"Failed to decode test-metadata.csv: {e}")
        return None

    if text.strip().startswith('<!doctype') or text.strip().startswith('<html'):
        logger.debug("Got HTML instead of test-metadata.csv, file doesn't exist")
        return None

    try:
        reader = csv.DictReader(io.StringIO(text))
        row = next(reader, None)
    except csv.Error as e:
        logger.debug(f"Failed to parse test-metadata.csv: {e}")
        return None

    if not row:
        return None

    return {k: (v or '').strip() for k, v in row.items() if k}


def fetch_record_metadata(base_url: str, variant: str) -> Optional[Dict[str, str]]:
    """
    Fetch and parse the record-metadata step's test-metadata.csv artifact.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant for artifact path

    Returns:
        Parsed CSV row dict, or None if the step/artifact isn't present
        (e.g. older jobs predating this step).
    """
    path = f"artifacts/{variant}/{RECORD_METADATA_STEP}/artifacts/{RECORD_METADATA_CSV_PATH}"
    content = fetch_artifact(base_url, path)
    row = parse_record_metadata_csv(content) if content else None
    if row:
        logger.debug(f"Found record-metadata CSV: {row}")
    else:
        logger.debug("record-metadata test-metadata.csv not found or unparseable")
    return row


def extract_build_info(prowjob_data: Dict) -> Dict:
    """
    Extract build/catalog information from prowjob.

    Args:
        prowjob_data: Parsed prowjob.json

    Returns:
        Dictionary with build information
    """
    env_vars = prowjob_data.get('env_vars', {})

    catalog_image = env_vars.get('CATALOG_SOURCE_IMAGE', '')

    build_info = {
        'catalog_source_image': catalog_image,
        'catalog_source_name': env_vars.get('CATALOG_SOURCE_NAME', ''),
    }

    # Parse catalog tag for version and timestamp (catalog_build_date is "not set" when no catalog)
    catalog_info = parse_catalog_tag(catalog_image)
    build_info.update(catalog_info)

    # Determine release stage from job name
    job_name = prowjob_data.get('job_name', '')
    build_info['release_stage'] = ''
    if 'downstream-candidate' in job_name:
        build_info['release_stage'] = 'candidate'
    elif 'downstream-release' in job_name:
        build_info['release_stage'] = 'release'

    return build_info


def extract_trigger_source(prowjob_data: Dict) -> str:
    """
    Extract trigger source for the job.

    Args:
        prowjob_data: Parsed prowjob.json

    Returns:
        Trigger source (periodic, presubmit, postsubmit, manual, konflux, rehearsal, etc.)
    """
    job_type = prowjob_data.get('type', '').lower()
    job_name = prowjob_data.get('job_name', '')

    # Check metadata
    labels = prowjob_data.get('metadata', {}).get('labels', {})
    annotations = prowjob_data.get('metadata', {}).get('annotations', {})

    # Check for Konflux trigger via gangway API
    # Konflux-submitted jobs have these annotations even when they appear as periodic
    if annotations.get('ci.openshift.io/executor') == 'gangway' and 'ci.openshift.io/konflux-repo' in annotations:
        return 'konflux'

    # Check for Konflux integration test label (older method)
    if 'prow.k8s.io/integration-test' in labels or 'konflux' in str(labels).lower():
        return 'konflux'

    # Check for rehearsal (presubmit PR testing)
    if 'rehearse' in job_name.lower():
        return 'rehearsal'

    # Check for clusterbot
    env_vars = prowjob_data.get('env_vars', {})
    if 'CLUSTER_TYPE' in env_vars or 'clusterbot' in str(annotations).lower():
        return 'clusterbot'

    # Standard Prow job types
    if job_type in ['periodic', 'presubmit', 'postsubmit']:
        return job_type

    return 'manual'


def extract_kata_rpm_version(base_url: str, variant: str) -> Optional[str]:
    """
    Extract Kata RPM version from job artifacts (legacy fallback).

    Prefer ``fetch_record_metadata``'s ``kata_rpm_version`` column -- it's
    captured straight from the KATA_RPM_VERSION env var by the
    record-metadata step, after the RPM install actually ran. This function
    is kept for jobs that predate that step: it parses this step's own
    build-log.txt (step name is "update-kata-rpm", not "get-kata-rpm" --
    that name never existed in the `release` step-registry, and this step
    never wrote a separate kata-rpm-version.txt file either).

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant for artifact path

    Returns:
        Kata RPM version string or None if not found
    """
    build_log_path = f"artifacts/{variant}/{UPDATE_KATA_RPM_STEP}/build-log.txt"
    content = fetch_artifact(base_url, build_log_path)

    if content:
        try:
            log_text = content.decode('utf-8', errors='ignore')
            # Don't try to parse if it's HTML
            if not (log_text.strip().startswith('<!doctype') or log_text.strip().startswith('<html')):
                # update-kata-rpm-commands.sh: echo "Target RPM: ${target_version}"
                # where target_version=$(rpm -qp ./kata-containers.rpm), e.g.
                # "kata-containers-3.17.0-3.rhaos4.19.el9.x86_64"
                match = re.search(r'Target RPM:\s+(\S+)', log_text)
                if match:
                    version = match.group(1)
                    logger.debug(f"Extracted Kata RPM version from log: {version}")
                    return version
        except Exception as e:
            logger.debug(f"Failed to parse build log: {e}")

    logger.debug("Kata RPM version not found (may be using node default, or INSTALL_KATA_RPM=false)")
    return None


def extract_catalog_from_extended_log(base_url: str, variant: str) -> Optional[str]:
    """
    Extract catalog source image from extended.log (legacy fallback).

    ``openshift-extended-test`` (OTP) was removed from the downstream-candidate
    Prow matrix's `test:` phase around 2026-10 (see
    wiki/concepts/osc-job-result-analysis.md in the osc-LLM-wiki and
    wiki/concepts/ci-testing-flow.md's correction note) and replaced by the
    `sandboxed-containers-operator-testsuites` chain, which never writes an
    extended.log. For any current job this will simply find nothing and
    return None -- it's kept only in case older job artifacts are analyzed.
    Prefer ``fetch_record_metadata`` for current jobs.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant for artifact path

    Returns:
        Catalog source image string or None if not found
    """
    extended_log_path = f"artifacts/{variant}/openshift-extended-test/artifacts/extended.log"
    content = fetch_artifact(base_url, extended_log_path)

    if content:
        try:
            log_text = content.decode('utf-8', errors='ignore')
            # Look for catalog source image patterns
            # Pattern: "catalog source image & tag: quay.io/.../osc-test-fbc:1.11.1-1765791442"
            match = re.search(r'catalog source image & tag:\s+([^\s]+)', log_text)
            if match:
                catalog_image = match.group(1)
                logger.debug(f"Found catalog source in extended.log: {catalog_image}")
                return catalog_image
        except Exception as e:
            logger.debug(f"Failed to parse extended.log for catalog source: {e}")

    return None


def extract_ocp_channel(base_url: str, variant: str) -> Optional[str]:
    """
    Extract OCP channel from ipi-install-install build-log.txt.

    Args:
        base_url: Base URL of the Prow job
        variant: Job variant for artifact path

    Returns:
        OCP channel string (e.g., "stable-4.20") or None if not found
    """
    build_log_path = f"artifacts/{variant}/ipi-install-install/build-log.txt"
    content = fetch_artifact(base_url, build_log_path)

    if content:
        try:
            log_text = content.decode('utf-8', errors='ignore')
            # Don't try to parse if it's HTML
            if not (log_text.strip().startswith('<!doctype') or log_text.strip().startswith('<html')):
                # Look for "Setting channel to" line which shows the final channel used
                match = re.search(r'Setting channel to ((?:stable|fast|candidate|eus)-\d+\.\d+)', log_text)
                if match:
                    channel = match.group(1)
                    logger.debug(f"Found OCP channel: {channel}")
                    return channel

                # Fallback: look for any channel pattern
                match = re.search(r'(stable|fast|candidate|eus)-\d+\.\d+', log_text)
                if match:
                    channel = match.group(0)
                    logger.debug(f"Found OCP channel (fallback): {channel}")
                    return channel
        except Exception as e:
            logger.debug(f"Failed to parse ipi-install-install build-log.txt for OCP channel: {e}")

    return None


def extract_metadata(prowjob_data: Dict, base_url: str) -> Dict:
    """
    Extract all metadata from prowjob and artifacts.

    Args:
        prowjob_data: Parsed prowjob.json with job_info
        base_url: Base URL for fetching artifacts

    Returns:
        Dictionary with all extracted metadata
    """
    job_name = prowjob_data.get('job_name', '')
    variant = extract_variant_from_job_name(job_name)

    metadata = {
        'provider': extract_provider(job_name),
        'workload_type': extract_workload_type(job_name),
        'ocp_version': extract_ocp_version(prowjob_data, base_url),
        'trigger_source': extract_trigger_source(prowjob_data),
        'variant': variant or 'unknown',
        'job_name': job_name,
        'build_id': prowjob_data.get('build_id', 'unknown'),
    }

    # Extract build information from prowjob.json env vars. Note this is
    # usually the *unresolved* CATALOG_SOURCE_IMAGE (e.g. a static "...:latest"
    # from the ci-operator config), not the real tag actually pulled at
    # runtime -- see fetch_record_metadata below for that.
    build_info = extract_build_info(prowjob_data)

    # If catalog source not in env vars, try the legacy extended.log fallback
    # (dead for any post-OTP-removal job; see extract_catalog_from_extended_log).
    if not build_info.get('catalog_source_image') and variant:
        catalog_from_log = extract_catalog_from_extended_log(base_url, variant)
        if catalog_from_log:
            build_info['catalog_source_image'] = catalog_from_log
            # Re-parse catalog tag with the newly found image
            catalog_info = parse_catalog_tag(catalog_from_log)
            build_info.update(catalog_info)

    build_info['catalog_build_date_source'] = 'prowjob-env' if build_info.get('catalog_source_image') else 'not set'

    metadata.update(build_info)

    # Extract Kata RPM version if variant is known (legacy per-step fallback;
    # may be overridden below by record-metadata, the authoritative source).
    if variant:
        kata_rpm_version = extract_kata_rpm_version(base_url, variant)
        if kata_rpm_version:
            metadata['kata_rpm_version'] = kata_rpm_version
            metadata['kata_rpm_source'] = 'installed'
        else:
            metadata['kata_rpm_version'] = 'node-default'
            metadata['kata_rpm_source'] = 'node'
    else:
        metadata['kata_rpm_version'] = 'unknown'
        metadata['kata_rpm_source'] = 'unknown'

    # Prefer the sandboxed-containers-operator-record-metadata step's CSV: it
    # runs after install-osc-operator resolved ":latest" to the real tag and
    # after update-kata-rpm actually installed the RPM, and it queries Quay
    # itself for build_time. This is what fixes KATA-6339 ("catalog isn't
    # always extracted and translated to a date" / "rpm version isn't always
    # captured") -- those legacy extractors above either read the unresolved
    # env var or a step path/name that no longer (or never) existed.
    if variant:
        record_metadata = fetch_record_metadata(base_url, variant)
        if record_metadata:
            catalog_tag = record_metadata.get('catalog_tag', '')
            build_time = record_metadata.get('build_time', '')
            if catalog_tag not in _RECORD_METADATA_PROBLEM_VALUES:
                metadata['full_tag'] = catalog_tag
                metadata.update(_parse_tag_version_info(catalog_tag))
                metadata['catalog_build_date_source'] = 'record-metadata'
                if build_time not in _RECORD_METADATA_PROBLEM_VALUES:
                    metadata['catalog_build_date'] = build_time

            kata_rpm_version = record_metadata.get('kata_rpm_version', '')
            if kata_rpm_version not in _RECORD_METADATA_PROBLEM_VALUES:
                metadata['kata_rpm_version'] = kata_rpm_version
                metadata['kata_rpm_source'] = 'record-metadata'

    # Extract OCP channel if variant is known
    if variant:
        ocp_channel = extract_ocp_channel(base_url, variant)
        if ocp_channel:
            metadata['ocp_channel'] = ocp_channel
        else:
            metadata['ocp_channel'] = 'unknown'
    else:
        metadata['ocp_channel'] = 'unknown'

    return metadata
