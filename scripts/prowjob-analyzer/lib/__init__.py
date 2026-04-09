"""
Prow Job Analyzer Library

This package provides modules for analyzing OpenShift Prow job results,
specifically tailored for OpenShift Sandboxed Containers (OSC) testing.
"""

__version__ = "1.0.0"

from .fetcher import (
    parse_prow_url,
    fetch_artifact,
    wait_for_artifacts,
    configure_artifact_source,
    clear_artifact_cache,
    is_local_artifact_mode,
    download_job_artifacts,
    write_job_artifacts_tarball,
    gcs_uri_from_gcsweb_https,
    gcs_uri_for_job_artifacts,
)
from .parser import parse_prowjob, parse_test_results, get_job_status
from .metadata_extractor import extract_metadata
from .failure_analyzer import analyze_failure
from .report_generator import generate_human_report, generate_json_report

__all__ = [
    "parse_prow_url",
    "fetch_artifact",
    "wait_for_artifacts",
    "configure_artifact_source",
    "clear_artifact_cache",
    "is_local_artifact_mode",
    "download_job_artifacts",
    "write_job_artifacts_tarball",
    "gcs_uri_from_gcsweb_https",
    "gcs_uri_for_job_artifacts",
    "parse_prowjob",
    "parse_test_results",
    "get_job_status",
    "extract_metadata",
    "analyze_failure",
    "generate_human_report",
    "generate_json_report",
]
