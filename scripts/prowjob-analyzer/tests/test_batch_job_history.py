"""Unit tests for batch_job_history.py."""

import sys
import os
import unittest
from unittest.mock import patch

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from batch_job_history import (
    JOB_NAME_TEMPLATE,
    fetch_one_job,
    iter_job_names,
    normalize_version,
    split_csv,
)


class TestNormalizeVersion(unittest.TestCase):
    def test_dotted_version_is_collapsed(self) -> None:
        self.assertEqual(normalize_version('4.20'), '420')

    def test_bare_version_is_unchanged(self) -> None:
        self.assertEqual(normalize_version('420'), '420')

    def test_strips_whitespace(self) -> None:
        self.assertEqual(normalize_version(' 4.22 '), '422')


class TestSplitCsv(unittest.TestCase):
    def test_splits_and_strips(self) -> None:
        self.assertEqual(split_csv('azure, aws ,aro'), ['azure', 'aws', 'aro'])

    def test_ignores_empty_entries(self) -> None:
        self.assertEqual(split_csv('azure,,aws,'), ['azure', 'aws'])

    def test_empty_string_gives_empty_list(self) -> None:
        self.assertEqual(split_csv(''), [])


class TestIterJobNames(unittest.TestCase):
    def test_cartesian_product_of_all_dimensions(self) -> None:
        names = list(iter_job_names(['420', '421'], ['azure', 'aws'], ['kata']))
        self.assertEqual(len(names), 4)
        self.assertIn(
            ('420', 'azure', 'kata', JOB_NAME_TEMPLATE.format(version='420', provider='azure', workload='kata')),
            names,
        )
        self.assertIn(
            ('421', 'aws', 'kata', JOB_NAME_TEMPLATE.format(version='421', provider='aws', workload='kata')),
            names,
        )

    def test_job_name_matches_known_pattern(self) -> None:
        _, _, _, job_name = next(iter_job_names(['422'], ['azure'], ['kata']))
        self.assertEqual(
            job_name,
            'periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-'
            'candidate422-azure-ipi-kata',
        )

    def test_custom_template(self) -> None:
        _, _, _, job_name = next(
            iter_job_names(['422'], ['azure'], ['kata'], template='prefix-{version}-{provider}-{workload}')
        )
        self.assertEqual(job_name, 'prefix-422-azure-kata')


class TestFetchOneJob(unittest.TestCase):
    def test_builds_url_and_converts_records(self) -> None:
        raw_build = {
            'SpyglassLink': '/view/gs/test-platform-results/logs/job/123',
            'ID': '123',
            'Started': '2026-10-15T00:05:15Z',
            'Duration': 60_000_000_000,
            'Result': 'SUCCESS',
        }
        with patch('batch_job_history.fetch_filtered_builds', return_value=[raw_build]) as mock_fetch:
            records = fetch_one_job('periodic-ci-foo', 'test-platform-results', 'ALL', None, None)

        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]['id'], '123')
        self.assertEqual(records[0]['url'], 'https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/123')

        # fetch_all=True / limit=None regardless of caller args: a batch run
        # always wants the whole --since/--until window, not just one page.
        _, kwargs = mock_fetch.call_args
        self.assertTrue(kwargs['fetch_all'])
        self.assertIsNone(kwargs['limit'])

    def test_propagates_errors_for_caller_to_handle(self) -> None:
        with patch('batch_job_history.fetch_filtered_builds', side_effect=RuntimeError('boom')):
            with self.assertRaises(RuntimeError):
                fetch_one_job('periodic-ci-foo', 'test-platform-results', 'ALL', None, None)


if __name__ == '__main__':
    unittest.main()
