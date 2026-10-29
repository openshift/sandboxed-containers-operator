"""Unit tests for metadata_extractor, focused on KATA-6339's two bugs:

- catalog build date not always extracted/translated to a date
- kata RPM version not always captured

Covers the new sandboxed-containers-operator-record-metadata CSV path (the
fix) and the legacy per-field extractors it supersedes (kept as fallback for
older jobs).
"""

import unittest
from unittest.mock import patch

from lib.metadata_extractor import (
    _parse_tag_version_info,
    extract_build_info,
    extract_catalog_from_extended_log,
    extract_kata_rpm_version,
    extract_metadata,
    fetch_record_metadata,
    parse_catalog_tag,
    parse_record_metadata_csv,
)


class TestParseTagVersionInfo(unittest.TestCase):
    def test_version_timestamp(self) -> None:
        info = _parse_tag_version_info('1.11.1-1765791442')
        self.assertEqual(info['base_version'], '1.11.1')
        self.assertEqual(info['timestamp'], '1765791442')
        self.assertTrue(info['catalog_build_date'].endswith('UTC'))

    def test_latest_has_no_date(self) -> None:
        info = _parse_tag_version_info('latest')
        self.assertEqual(info['base_version'], 'latest')
        self.assertEqual(info['catalog_build_date'], 'unknown')

    def test_v_prefixed_version(self) -> None:
        info = _parse_tag_version_info('v1.11.1')
        self.assertEqual(info['base_version'], '1.11.1')

    def test_plain_version(self) -> None:
        info = _parse_tag_version_info('1.11.1')
        self.assertEqual(info['base_version'], '1.11.1')

    def test_git_sha(self) -> None:
        info = _parse_tag_version_info('abc123def456')
        self.assertEqual(info['base_version'], 'abc123def456')

    def test_sha256_digest_tag_strips_prefix(self) -> None:
        info = _parse_tag_version_info('sha256:' + 'a' * 64)
        self.assertEqual(info['base_version'], 'a' * 64)

    def test_invalid_timestamp_is_flagged(self) -> None:
        # 99999999999999 overflows datetime.fromtimestamp on most platforms
        info = _parse_tag_version_info('1.0.0-99999999999999')
        self.assertTrue(info['catalog_build_date'].startswith('invalid-timestamp-'))


class TestParseCatalogTag(unittest.TestCase):
    """Legacy prowjob.json env-var path. Still used when the
    record-metadata CSV isn't available (older jobs)."""

    def test_empty_image_is_not_set(self) -> None:
        info = parse_catalog_tag('')
        self.assertEqual(info['catalog_build_date'], 'not set')

    def test_static_latest_tag_has_no_date(self) -> None:
        # The common real-world case from ci-operator config: a literal
        # "...osc-test-fbc:latest" -- this is KATA-6339's bug 1 root cause,
        # since this is the *unresolved* value; see fetch_record_metadata.
        info = parse_catalog_tag('quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest')
        self.assertEqual(info['full_tag'], 'latest')
        self.assertEqual(info['catalog_build_date'], 'unknown')

    def test_version_timestamp_tag(self) -> None:
        info = parse_catalog_tag('quay.io/example/osc-test-fbc:1.11.1-1765791442')
        self.assertEqual(info['base_version'], '1.11.1')
        self.assertEqual(info['timestamp'], '1765791442')
        self.assertTrue(info['catalog_build_date'].endswith('UTC'))

    @patch('lib.metadata_extractor.fetch_build_date_from_quay')
    def test_digest_image_delegates_to_quay(self, mock_fetch) -> None:
        mock_fetch.return_value = '2026-01-01 00:00:00 UTC'
        info = parse_catalog_tag('quay.io/example/repo@sha256:' + 'a' * 64)
        self.assertEqual(info['base_version'], 'a' * 64)
        self.assertEqual(info['catalog_build_date'], '2026-01-01 00:00:00 UTC')


class TestExtractCatalogFromExtendedLog(unittest.TestCase):
    """OTP's openshift-extended-test step was removed from the Prow matrix;
    this legacy fallback should simply find nothing for current jobs."""

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_no_artifact_returns_none(self, mock_fetch) -> None:
        mock_fetch.return_value = None
        self.assertIsNone(extract_catalog_from_extended_log('http://x', 'aws-ipi-kata'))

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_html_placeholder_returns_none(self, mock_fetch) -> None:
        mock_fetch.return_value = b'<!doctype html><html></html>'
        self.assertIsNone(extract_catalog_from_extended_log('http://x', 'aws-ipi-kata'))

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_still_parses_when_present_for_old_jobs(self, mock_fetch) -> None:
        mock_fetch.return_value = (
            b'some log text\n'
            b'catalog source image & tag: quay.io/example/osc-test-fbc:1.11.1-1765791442\n'
        )
        result = extract_catalog_from_extended_log('http://x', 'aws-ipi-kata')
        self.assertEqual(result, 'quay.io/example/osc-test-fbc:1.11.1-1765791442')


class TestExtractKataRpmVersion(unittest.TestCase):
    """Legacy fallback. Must use the real step name
    (sandboxed-containers-operator-update-kata-rpm) and the real log line
    ("Target RPM: ...") -- KATA-6339 bug 2's root cause was both being wrong."""

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_no_build_log_returns_none(self, mock_fetch) -> None:
        mock_fetch.return_value = None
        self.assertIsNone(extract_kata_rpm_version('http://x', 'aws-ipi-kata'))

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_parses_target_rpm_line(self, mock_fetch) -> None:
        mock_fetch.return_value = (
            b'Get the authentication credentials for Brew\n'
            b'Downloaded RPM: -rw-r--r-- 1 root root 5.2M kata-containers.rpm\n'
            b'Target RPM: kata-containers-3.17.0-3.rhaos4.19.el9.x86_64\n'
            b'RPM md5sum: abc123\n'
        )
        version = extract_kata_rpm_version('http://x', 'aws-ipi-kata')
        self.assertEqual(version, 'kata-containers-3.17.0-3.rhaos4.19.el9.x86_64')

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_fetches_from_update_kata_rpm_step_not_get_kata_rpm(self, mock_fetch) -> None:
        mock_fetch.return_value = b'Target RPM: kata-containers-3.17.0-3.el9.x86_64\n'
        extract_kata_rpm_version('http://x', 'aws-ipi-kata')
        called_path = mock_fetch.call_args[0][1]
        self.assertIn('sandboxed-containers-operator-update-kata-rpm', called_path)
        self.assertNotIn('get-kata-rpm', called_path)
        # build-log.txt sits directly under the step dir, not nested under
        # another "artifacts/" level (that nesting is only for named
        # artifact files like JUnit XML / CSVs).
        self.assertEqual(
            called_path,
            'artifacts/aws-ipi-kata/sandboxed-containers-operator-update-kata-rpm/build-log.txt',
        )

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_html_placeholder_returns_none(self, mock_fetch) -> None:
        mock_fetch.return_value = b'<!doctype html><html></html>'
        self.assertIsNone(extract_kata_rpm_version('http://x', 'aws-ipi-kata'))


class TestParseRecordMetadataCsv(unittest.TestCase):
    def _csv(self, **overrides) -> bytes:
        row = {
            'timestamp': '2026-10-29T12:00:00Z',
            'catalog_tag': '1.11.1-1765791442',
            'build_time': '2026-10-27T08:00:00Z',
            'cloud_provider': 'aws',
            'ocp_version': '4.19.5',
            'prow_job_url': 'https://prow.ci.openshift.org/view/gs/.../123',
            'workload_to_test': 'kata',
            'kata_rpm_version': 'kata-containers-3.17.0-3.rhaos4.19.el9.x86_64',
        }
        row.update(overrides)
        header = ','.join(row.keys())
        data = ','.join(row.values())
        return f"{header}\n{data}\n".encode('utf-8')

    def test_none_content_returns_none(self) -> None:
        self.assertIsNone(parse_record_metadata_csv(b''))
        self.assertIsNone(parse_record_metadata_csv(None))

    def test_html_placeholder_returns_none(self) -> None:
        self.assertIsNone(parse_record_metadata_csv(b'<!doctype html><html></html>'))

    def test_parses_happy_path_row(self) -> None:
        row = parse_record_metadata_csv(self._csv())
        self.assertEqual(row['catalog_tag'], '1.11.1-1765791442')
        self.assertEqual(row['build_time'], '2026-10-27T08:00:00Z')
        self.assertEqual(row['kata_rpm_version'], 'kata-containers-3.17.0-3.rhaos4.19.el9.x86_64')

    def test_header_only_no_data_row_returns_none(self) -> None:
        header = 'timestamp,catalog_tag,build_time,cloud_provider,ocp_version,prow_job_url,workload_to_test,kata_rpm_version\n'
        self.assertIsNone(parse_record_metadata_csv(header.encode('utf-8')))

    def test_problem_values_round_trip_as_is(self) -> None:
        # record-metadata-commands.sh writes these literal strings when it
        # can't determine a value; parsing must preserve them so
        # extract_metadata can recognize and skip them.
        row = parse_record_metadata_csv(self._csv(catalog_tag='image not set', build_time='cannot be found'))
        self.assertEqual(row['catalog_tag'], 'image not set')
        self.assertEqual(row['build_time'], 'cannot be found')


class TestFetchRecordMetadata(unittest.TestCase):
    @patch('lib.metadata_extractor.fetch_artifact')
    def test_fetches_correct_path(self, mock_fetch) -> None:
        mock_fetch.return_value = None
        fetch_record_metadata('http://x', 'aws-ipi-kata')
        called_path = mock_fetch.call_args[0][1]
        self.assertEqual(
            called_path,
            'artifacts/aws-ipi-kata/sandboxed-containers-operator-record-metadata/artifacts/test-metadata.csv',
        )

    @patch('lib.metadata_extractor.fetch_artifact')
    def test_missing_artifact_returns_none(self, mock_fetch) -> None:
        mock_fetch.return_value = None
        self.assertIsNone(fetch_record_metadata('http://x', 'aws-ipi-kata'))


class TestExtractMetadataIntegration(unittest.TestCase):
    """End-to-end: extract_metadata() should prefer record-metadata's CSV
    over the legacy env-var/build-log extraction for both KATA-6339 fields."""

    def _prowjob_data(self, catalog_image='quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest'):
        return {
            'job_name': 'periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-kata',
            'build_id': '12345',
            'type': 'periodic',
            'env_vars': {'CATALOG_SOURCE_IMAGE': catalog_image},
            'metadata': {'labels': {}, 'annotations': {}},
        }

    @patch('lib.metadata_extractor.extract_ocp_channel', return_value=None)
    @patch('lib.metadata_extractor.extract_kata_rpm_version', return_value=None)
    @patch('lib.metadata_extractor.extract_ocp_version_from_release_artifact', return_value=None)
    @patch('lib.metadata_extractor.fetch_record_metadata')
    def test_record_metadata_overrides_unresolved_latest_tag(
        self, mock_record_metadata, mock_ocp, mock_rpm, mock_channel
    ) -> None:
        # Static ":latest" in prowjob.json env vars (today's buggy case:
        # parse_catalog_tag alone would leave catalog_build_date "unknown").
        mock_record_metadata.return_value = {
            'catalog_tag': '1.11.1-1765791442',
            'build_time': '2026-10-27T08:00:00Z',
            'kata_rpm_version': 'kata-containers-3.17.0-3.rhaos4.19.el9.x86_64',
        }

        metadata = extract_metadata(self._prowjob_data(), 'http://x')

        self.assertEqual(metadata['full_tag'], '1.11.1-1765791442')
        self.assertEqual(metadata['base_version'], '1.11.1')
        self.assertEqual(metadata['catalog_build_date'], '2026-10-27T08:00:00Z')
        self.assertEqual(metadata['catalog_build_date_source'], 'record-metadata')
        self.assertEqual(metadata['kata_rpm_version'], 'kata-containers-3.17.0-3.rhaos4.19.el9.x86_64')
        self.assertEqual(metadata['kata_rpm_source'], 'record-metadata')

    @patch('lib.metadata_extractor.extract_ocp_channel', return_value=None)
    @patch('lib.metadata_extractor.extract_kata_rpm_version', return_value=None)
    @patch('lib.metadata_extractor.extract_ocp_version_from_release_artifact', return_value=None)
    @patch('lib.metadata_extractor.fetch_record_metadata', return_value=None)
    def test_falls_back_to_legacy_when_record_metadata_absent(
        self, mock_record_metadata, mock_ocp, mock_rpm, mock_channel
    ) -> None:
        # Older job predating the record-metadata step: no CSV at all.
        metadata = extract_metadata(
            self._prowjob_data('quay.io/example/osc-test-fbc:1.11.1-1765791442'), 'http://x'
        )

        self.assertEqual(metadata['full_tag'], '1.11.1-1765791442')
        self.assertEqual(metadata['base_version'], '1.11.1')
        self.assertTrue(metadata['catalog_build_date'].endswith('UTC'))
        self.assertEqual(metadata['catalog_build_date_source'], 'prowjob-env')
        self.assertEqual(metadata['kata_rpm_version'], 'node-default')
        self.assertEqual(metadata['kata_rpm_source'], 'node')

    @patch('lib.metadata_extractor.extract_catalog_from_extended_log', return_value=None)
    @patch('lib.metadata_extractor.extract_ocp_channel', return_value=None)
    @patch('lib.metadata_extractor.extract_kata_rpm_version', return_value='legacy-parsed-version')
    @patch('lib.metadata_extractor.extract_ocp_version_from_release_artifact', return_value=None)
    @patch('lib.metadata_extractor.fetch_record_metadata')
    def test_record_metadata_problem_values_fall_back_to_legacy(
        self, mock_record_metadata, mock_ocp, mock_rpm, mock_channel, mock_extended_log
    ) -> None:
        # CSV present but its own values are problem placeholders (e.g. the
        # record-metadata step itself couldn't resolve them) -- must not
        # clobber a usable legacy-extracted value with an empty one.
        mock_record_metadata.return_value = {
            'catalog_tag': 'image not set',
            'build_time': 'cannot be found',
            'kata_rpm_version': '',
        }

        metadata = extract_metadata(self._prowjob_data(''), 'http://x')

        self.assertEqual(metadata['catalog_build_date'], 'not set')
        self.assertEqual(metadata['catalog_build_date_source'], 'not set')
        self.assertEqual(metadata['kata_rpm_version'], 'legacy-parsed-version')
        self.assertEqual(metadata['kata_rpm_source'], 'installed')


if __name__ == "__main__":
    unittest.main()
