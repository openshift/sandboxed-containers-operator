"""Unit tests for job_history.py (Workstream G1)."""

import sys
import os
import unittest
from datetime import datetime, timezone

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from job_history import (
    _ALL_BUILDS_RE,
    _OLDER_LINK_RE,
    _SHOWING_RE,
    _matches_filters,
    _parse_date_arg,
    _parse_started,
    build_history_url,
    build_to_record,
)


class TestBuildHistoryUrl(unittest.TestCase):
    def test_full_url_used_as_is(self) -> None:
        url = 'https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/some-job?buildId=123'
        self.assertEqual(build_history_url(url), url)

    def test_bare_job_name_builds_default_url(self) -> None:
        self.assertEqual(
            build_history_url('periodic-ci-foo'),
            'https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-foo',
        )

    def test_custom_bucket(self) -> None:
        self.assertEqual(
            build_history_url('periodic-ci-foo', bucket='other-bucket'),
            'https://prow.ci.openshift.org/job-history/gs/other-bucket/logs/periodic-ci-foo',
        )


class TestParseDateArg(unittest.TestCase):
    def test_bare_date_assumed_midnight_utc(self) -> None:
        dt = _parse_date_arg('2026-10-20')
        self.assertEqual(dt, datetime(2026, 10, 20, tzinfo=timezone.utc))

    def test_full_timestamp(self) -> None:
        dt = _parse_date_arg('2026-10-20T12:30:00Z')
        self.assertEqual(dt, datetime(2026, 10, 20, 12, 30, tzinfo=timezone.utc))

    def test_invalid_raises(self) -> None:
        with self.assertRaises(Exception):
            _parse_date_arg('not-a-date')


class TestParseStarted(unittest.TestCase):
    def test_parses_z_suffix(self) -> None:
        dt = _parse_started('2026-10-15T00:05:15Z')
        self.assertEqual(dt, datetime(2026, 10, 15, 0, 5, 15, tzinfo=timezone.utc))

    def test_empty_returns_none(self) -> None:
        self.assertIsNone(_parse_started(''))

    def test_invalid_returns_none(self) -> None:
        self.assertIsNone(_parse_started('not-a-timestamp'))


class TestMatchesFilters(unittest.TestCase):
    def _build(self, result: str = 'FAILURE', started: str = '2026-10-15T00:05:15Z') -> dict:
        return {'Result': result, 'Started': started}

    def test_result_all_matches_everything(self) -> None:
        self.assertTrue(_matches_filters(self._build('SUCCESS'), 'ALL', None, None))
        self.assertTrue(_matches_filters(self._build('FAILURE'), 'ALL', None, None))

    def test_result_filter(self) -> None:
        self.assertTrue(_matches_filters(self._build('FAILURE'), 'FAILURE', None, None))
        self.assertFalse(_matches_filters(self._build('SUCCESS'), 'FAILURE', None, None))

    def test_since_excludes_earlier(self) -> None:
        since = datetime(2026, 10, 16, tzinfo=timezone.utc)
        self.assertFalse(_matches_filters(self._build(), 'ALL', since, None))

    def test_since_includes_later_or_equal(self) -> None:
        since = datetime(2026, 10, 14, tzinfo=timezone.utc)
        self.assertTrue(_matches_filters(self._build(), 'ALL', since, None))

    def test_until_excludes_later(self) -> None:
        until = datetime(2026, 10, 14, tzinfo=timezone.utc)
        self.assertFalse(_matches_filters(self._build(), 'ALL', None, until))

    def test_missing_started_excluded_when_since_set(self) -> None:
        since = datetime(2026, 10, 1, tzinfo=timezone.utc)
        self.assertFalse(_matches_filters(self._build(started=''), 'ALL', since, None))


class TestBuildToRecord(unittest.TestCase):
    def test_converts_duration_ns_to_seconds_and_joins_url(self) -> None:
        build = {
            'SpyglassLink': '/view/gs/test-platform-results/logs/job/123',
            'ID': '123',
            'Started': '2026-10-15T00:05:15Z',
            'Duration': 90_000_000_000,
            'Result': 'FAILURE',
        }
        record = build_to_record(build)
        self.assertEqual(record['id'], '123')
        self.assertEqual(record['duration_seconds'], 90)
        self.assertEqual(record['result'], 'FAILURE')
        self.assertEqual(
            record['url'],
            'https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/123',
        )

    def test_missing_spyglass_link_gives_empty_url(self) -> None:
        record = build_to_record({'ID': '1', 'Started': '', 'Duration': 0, 'Result': ''})
        self.assertEqual(record['url'], '')
        self.assertEqual(record['duration_seconds'], 0)


class TestHtmlRegexes(unittest.TestCase):
    """Regex extraction against a minimal but structurally faithful page fragment."""

    _SAMPLE_HTML = '''
<script type="text/javascript">
  var allBuilds = [{"SpyglassLink":"/view/gs/test-platform-results/logs/job/222","ID":"222","Started":"2026-10-20T00:05:00Z","Duration":100000000000,"Result":"SUCCESS","Refs":null},{"SpyglassLink":"/view/gs/test-platform-results/logs/job/111","ID":"111","Started":"2026-10-19T00:05:00Z","Duration":50000000000,"Result":"FAILURE","Refs":null}];
</script>
<table>
  <tr>
    <td><a href="/job-history/gs/test-platform-results/logs/job?buildId=111">&lt;- Older Runs</a></td>
  </tr>
</table>
<p>Showing 2/40 results</p>
'''

    def test_all_builds_regex_extracts_valid_json(self) -> None:
        import json
        m = _ALL_BUILDS_RE.search(self._SAMPLE_HTML)
        self.assertIsNotNone(m)
        data = json.loads(m.group(1))
        self.assertEqual(len(data), 2)
        self.assertEqual(data[0]['ID'], '222')

    def test_older_link_regex(self) -> None:
        m = _OLDER_LINK_RE.search(self._SAMPLE_HTML)
        self.assertIsNotNone(m)
        self.assertIn('buildId=111', m.group(1))

    def test_showing_regex(self) -> None:
        m = _SHOWING_RE.search(self._SAMPLE_HTML)
        self.assertIsNotNone(m)
        self.assertEqual((int(m.group(1)), int(m.group(2))), (2, 40))

    def test_older_link_absent_on_last_page(self) -> None:
        html_no_older = self._SAMPLE_HTML.split('<table>')[0] + '<p>Showing 2/2 results</p>'
        self.assertIsNone(_OLDER_LINK_RE.search(html_no_older))


if __name__ == '__main__':
    unittest.main()
