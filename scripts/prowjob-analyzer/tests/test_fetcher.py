"""Unit tests for fetcher helpers."""

import os
import shutil
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from lib.fetcher import (
    clear_artifact_cache,
    configure_artifact_source,
    fetch_artifact,
    gcs_uri_from_gcsweb_https,
    get_step_directories,
    go_duration_to_seconds,
    write_job_artifacts_tarball,
    parse_build_log_duration_fallback,
    parse_ginkgo_failed_durations_max,
    parse_test_step_duration_from_build_log,
    summarize_openshift_extended_test_build_log,
)


class TestGoDurationToSeconds(unittest.TestCase):
    def test_empty_or_whitespace_returns_none(self) -> None:
        self.assertIsNone(go_duration_to_seconds(""))
        self.assertIsNone(go_duration_to_seconds("   "))

    def test_hours_minutes_seconds(self) -> None:
        self.assertEqual(go_duration_to_seconds("1h"), 3600)
        self.assertEqual(go_duration_to_seconds("30m"), 30 * 60)
        self.assertEqual(go_duration_to_seconds("45s"), 45)
        self.assertEqual(go_duration_to_seconds("1h30m5s"), 3600 + 30 * 60 + 5)

    def test_fractional_components(self) -> None:
        self.assertEqual(go_duration_to_seconds("1.5h"), int(round(1.5 * 3600)))
        self.assertEqual(go_duration_to_seconds("2m30.5s"), int(round(2 * 60 + 30.5)))

    def test_milliseconds_not_minutes(self) -> None:
        # "813ms" must not be parsed as 813 minutes; ms is stripped first.
        self.assertEqual(go_duration_to_seconds("813ms"), int(round(0.813)))

    def test_milliseconds_sum(self) -> None:
        self.assertEqual(
            go_duration_to_seconds("500ms300ms"),
            int(round(0.8)),
        )

    def test_zero_or_negative_total_returns_none(self) -> None:
        self.assertIsNone(go_duration_to_seconds("0s"))
        self.assertIsNone(go_duration_to_seconds("0h"))

    def test_combined_ginkgo_style(self) -> None:
        self.assertEqual(go_duration_to_seconds("3h1m37s"), 3 * 3600 + 60 + 37)


class TestSummarizeOpenshiftExtendedTestBuildLog(unittest.TestCase):
    def test_empty_returns_none(self) -> None:
        self.assertIsNone(summarize_openshift_extended_test_build_log(""))

    def test_error_summary_last_match(self) -> None:
        log = """\
line1
error: 1 fail, 10 pass, 2 skip
ERROR: 3 fail, 5 pass, 0 skip
"""
        self.assertEqual(
            summarize_openshift_extended_test_build_log(log),
            "3 fail, 5 pass, 0 skip",
        )

    def test_started_tuple_when_no_error_line(self) -> None:
        log = """\
started: (0/1/100)
started: (2/40/200)
"""
        self.assertEqual(
            summarize_openshift_extended_test_build_log(log),
            "(2/40/200)",
        )

    def test_error_takes_precedence_over_started(self) -> None:
        log = """\
started: (9/9/9)
error: 1 fail, 2 pass, 3 skip
"""
        self.assertEqual(
            summarize_openshift_extended_test_build_log(log),
            "1 fail, 2 pass, 3 skip",
        )

    def test_no_matching_lines_returns_none(self) -> None:
        self.assertIsNone(
            summarize_openshift_extended_test_build_log("just some build output\n"),
        )


class TestParseTestStepDurationFromBuildLog(unittest.TestCase):
    """``error: X fail, Y pass, Z skip (DURATION)`` — last match, duration → seconds."""

    def test_empty_returns_none(self) -> None:
        self.assertIsNone(parse_test_step_duration_from_build_log(""))

    def test_requires_parenthesized_duration(self) -> None:
        self.assertIsNone(
            parse_test_step_duration_from_build_log(
                "error: 1 fail, 2 pass, 3 skip\n",
            )
        )

    def test_parses_duration_seconds(self) -> None:
        log = "error: 0 fail, 1 pass, 0 skip (90s)\n"
        self.assertEqual(parse_test_step_duration_from_build_log(log), 90)

    def test_last_match_wins(self) -> None:
        log = """\
error: 1 fail, 0 pass, 0 skip (10m)
ERROR: 0 fail, 5 pass, 0 skip (2h)
"""
        self.assertEqual(
            parse_test_step_duration_from_build_log(log),
            2 * 3600,
        )

    def test_zero_duration_returns_none(self) -> None:
        log = "error: 1 fail, 0 pass, 0 skip (0s)\n"
        self.assertIsNone(parse_test_step_duration_from_build_log(log))


class TestParseGinkgoFailedDurationsMax(unittest.TestCase):
    """``failed: (DURATION)`` at line start — longest positive duration in seconds."""

    def test_empty_returns_none(self) -> None:
        self.assertIsNone(parse_ginkgo_failed_durations_max(""))

    def test_no_failed_lines_returns_none(self) -> None:
        self.assertIsNone(
            parse_ginkgo_failed_durations_max("passed: (3h)\nerror: (1h)\n"),
        )

    def test_single_line(self) -> None:
        log = "failed: (1h30m)\n"
        self.assertEqual(parse_ginkgo_failed_durations_max(log), 3600 + 30 * 60)

    def test_takes_maximum(self) -> None:
        log = """\
failed: (10m)
  failed: (2h)
failed: (30s)
"""
        self.assertEqual(parse_ginkgo_failed_durations_max(log), 2 * 3600)

    def test_ignores_zero_duration(self) -> None:
        log = "failed: (0s)\n"
        self.assertIsNone(parse_ginkgo_failed_durations_max(log))

    def test_line_must_start_with_failed(self) -> None:
        log = "message failed: (5h)\n"
        self.assertIsNone(parse_ginkgo_failed_durations_max(log))


class TestParseBuildLogDurationFallback(unittest.TestCase):
    """Parenthesized Go durations in log tail; skips Prow entrypoint noise lines."""

    def test_empty_returns_none(self) -> None:
        self.assertIsNone(parse_build_log_duration_fallback(""))

    def test_picks_max_parenthesized_duration(self) -> None:
        log = """\
phase complete (5m)
wrapper (2h30m)
"""
        self.assertEqual(parse_build_log_duration_fallback(log), 2 * 3600 + 30 * 60)

    def test_skips_prow_entrypoint_noise_line(self) -> None:
        log = """\
normal (30m)
{"component": "entrypoint", "x": "y"} (999h)
footer (1h)
"""
        self.assertEqual(parse_build_log_duration_fallback(log), 3600)

    def test_skips_grace_period_line(self) -> None:
        log = """\
suite (40m)
grace period 10m0s remaining (99h)
tail (15m)
"""
        self.assertEqual(parse_build_log_duration_fallback(log), 40 * 60)

    def test_ignores_non_duration_parens(self) -> None:
        log = "counts (1/2/3) and (0s)\n"
        self.assertIsNone(parse_build_log_duration_fallback(log))


class TestGcsUriFromGcsweb(unittest.TestCase):
    def test_gcsweb_https_to_gs(self) -> None:
        url = 'https://gcsweb.example/gcs/test-platform-results/logs/job-name/12345'
        self.assertEqual(
            gcs_uri_from_gcsweb_https(url),
            'gs://test-platform-results/logs/job-name/12345',
        )

    def test_already_gs_uri(self) -> None:
        self.assertEqual(
            gcs_uri_from_gcsweb_https('gs://b/prefix'),
            'gs://b/prefix',
        )


class TestArtifactCacheAndLocal(unittest.TestCase):
    def tearDown(self) -> None:
        configure_artifact_source(None)
        clear_artifact_cache()

    @patch('lib.fetcher._fetch_artifact_http')
    def test_http_fetch_cached_second_call_skips_network(self, mock_http) -> None:
        mock_http.return_value = b'{"ok": true}'
        base = 'https://example.com/view/gs/bucket/logs/job/123'
        fetch_artifact(base, 'prowjob.json')
        fetch_artifact(base, 'prowjob.json')
        self.assertEqual(mock_http.call_count, 1)

    def test_local_directory_reads_file(self) -> None:
        root = tempfile.mkdtemp()
        try:
            os.makedirs(os.path.join(root, 'artifacts', 'aws-ipi-peerpods', 'openshift-extended-test'))
            with open(os.path.join(root, 'prowjob.json'), 'w', encoding='utf-8') as f:
                f.write('{"status":{}}')
            with open(
                os.path.join(root, 'artifacts', 'aws-ipi-peerpods', 'openshift-extended-test', 'x.txt'),
                'wb',
            ) as f:
                f.write(b'hello')
            configure_artifact_source(root)
            self.assertEqual(fetch_artifact('http://ignored', 'prowjob.json'), b'{"status":{}}')
            self.assertEqual(
                fetch_artifact('http://ignored', 'artifacts/aws-ipi-peerpods/openshift-extended-test/x.txt'),
                b'hello',
            )
            steps = get_step_directories('http://ignored', 'aws-ipi-peerpods')
            self.assertIn('openshift-extended-test', steps)
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_tar_gz_extracts_and_reads(self) -> None:
        root = tempfile.mkdtemp()
        try:
            inner = os.path.join(root, 'jobdir')
            os.makedirs(inner)
            pj = os.path.join(inner, 'prowjob.json')
            with open(pj, 'w', encoding='utf-8') as f:
                f.write('{}')
            tar_path = os.path.join(root, 'a.tar.gz')
            with tarfile.open(tar_path, 'w:gz') as tf:
                tf.add(inner, arcname='jobdir')
            configure_artifact_source(tar_path)
            self.assertEqual(fetch_artifact('http://x', 'prowjob.json'), b'{}')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_write_job_artifacts_tarball_roundtrip(self) -> None:
        root = tempfile.mkdtemp()
        try:
            job = os.path.join(root, '12345')
            os.makedirs(job)
            with open(os.path.join(job, 'prowjob.json'), 'wb') as f:
                f.write(b'{}')
            out = os.path.join(root, '12345.tar.gz')
            write_job_artifacts_tarball(job, out)
            self.assertTrue(os.path.isfile(out))
            with tarfile.open(out, 'r:gz') as tf:
                names = tf.getnames()
            self.assertTrue(any(n.endswith('prowjob.json') for n in names))
        finally:
            shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    unittest.main()
