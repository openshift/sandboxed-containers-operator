"""
Unit tests for lib.report_generator's CSV/markdown result-categories views.

Per the prowjob-analyzer plan (§6.1), these tests construct their synthetic
fixtures as a ``report_data`` dict (i.e. what ``build_report_data()`` would
have produced / what ``generate_json_report()`` would serialize) and feed it
to ``generate_csv_report()``/``generate_human_report()`` -- not CSV-only
fixtures -- so the tests themselves enforce "CSV/markdown are views of the
JSON structure, not a parallel computation."
"""

import csv
import io
import unittest
from unittest.mock import patch

from lib.failure_analyzer import _build_suite_category, build_result_categories
from lib.fetcher import TESTSUITES_SUITE_STEPS
from lib.report_generator import (
    build_report_data,
    generate_csv_report,
    generate_human_report,
    generate_result_categories_section,
    generate_testsuites_section,
)


_CATEGORY_FIELDS = ('status', 'time', 'failed_steps', 'primary_pattern', 'confidence', 'root_cause')


def _category(**overrides) -> dict:
    base = {field: '' for field in _CATEGORY_FIELDS}
    base.update(overrides)
    return base


def _all_pass_categories() -> dict:
    return {
        'infra': _category(status='SUCCESS', time='00:05'),
        'overall': _category(status='SUCCESS', time='01:30'),
        'smoke': _category(status='SUCCESS', time='00:01'),
        'osc': _category(status='SUCCESS', time='00:10'),
        'kata_upstream': _category(status='SUCCESS', time='00:08'),
        'caa': _category(status='SUCCESS', time='00:12'),
    }


def _infra_only_failure_categories() -> dict:
    """
    When infra fails, build_result_categories() reports every other
    category as 'skipped' (ci-operator skips the test:-phase gate, and the
    post:-phase testsuites chain doesn't produce a meaningful result
    either) -- see build_result_categories()'s infra-failure short-circuit.
    """
    cats = _all_pass_categories()
    cats['infra'] = _category(
        status='FAILURE', time='00:05', failed_steps='ipi-install-install',
        primary_pattern='network', confidence='medium',
        root_cause='Network connectivity issues',
    )
    for key in ('overall', 'smoke', 'osc', 'kata_upstream', 'caa'):
        cats[key] = _category(status='skipped')
    return cats


def _every_category_failing() -> dict:
    return {
        'infra': _category(
            status='FAILURE', time='00:05', failed_steps='ipi-install-install',
            primary_pattern='network', confidence='medium', root_cause='Network connectivity issues',
        ),
        'overall': _category(
            status='FAILURE', time='01:30', failed_steps='timeout',
            root_cause='The job exceeded its execution timeout.',
        ),
        'smoke': _category(
            status='FAILURE', time='00:01',
            failed_steps='sandboxed-containers-operator-testsuites-gate',
            primary_pattern='timeout', confidence='medium',
            root_cause='Test execution exceeded time limits',
        ),
        'osc': _category(
            status='FAILURE', time='00:10',
            failed_steps='sandboxed-containers-operator-testsuites-osc',
            root_cause='Failed test case(s): C00349.',
        ),
        'kata_upstream': _category(
            status='FAILURE', time='00:08',
            failed_steps='sandboxed-containers-operator-testsuites-kata-upstream',
            root_cause='Failed test case(s): C00500.',
        ),
        'caa': _category(
            status='FAILURE', time='00:12',
            failed_steps='sandboxed-containers-operator-testsuites-caa',
            root_cause='Failed test case(s): C00600.',
        ),
    }


def _make_report_data(result_categories: dict, status: str = 'success', state: str = 'success') -> dict:
    """Build a canonical report_data dict the way dig.py/build_report_data() would."""
    prowjob_data = {
        'state': state,
        'duration_seconds': 5400,
        'start_time': '2026-01-01T00:00:00Z',
        'completion_time': '2026-01-01T01:30:00Z',
    }
    metadata = {
        'job_name': 'job',
        'build_id': '123',
        'trigger_source': 'manual',
        'provider': 'AWS',
        'ocp_version': '4.22',
        'workload_type': 'peerpods',
        'kata_rpm_version': 'node-default',
        'variant': 'aws-ipi-peerpods',
        'catalog_full_tag': 'sha256:deadbeef',
        'catalog_build_date': '2026-01-01',
    }
    return build_report_data(
        prowjob_data,
        metadata,
        status,
        None,
        None,
        'https://prow.ci.openshift.org/view/gs/bucket/logs/job/123',
        test_elapsed_time='00:20',
        suite_summaries=None,
        result_categories=result_categories,
    )


def _parse_csv_line(line: str):
    return next(csv.reader(io.StringIO(line)))


_EXPECTED_BASE_HEADER = [
    'trigger', 'start_time', 'catalog_full_tag', 'catalog_build_date',
    'provider', 'ocp_version', 'prowjob_url', 'workload_type', 'kata_rpm_version',
]

_EXPECTED_STATUS_HEADER = [
    'infra_status', 'overall_status', 'smoke_status',
    'osc_status', 'kata_upstream_status', 'caa_status',
]

_EXPECTED_DETAIL_HEADER = []
for _cat in ('infra', 'overall', 'smoke', 'osc', 'kata_upstream', 'caa'):
    _EXPECTED_DETAIL_HEADER.extend([
        f'{_cat}_time', f'{_cat}_failed_steps', f'{_cat}_primary_pattern',
        f'{_cat}_confidence', f'{_cat}_root_cause',
    ])

_EXPECTED_HEADER = _EXPECTED_BASE_HEADER + _EXPECTED_STATUS_HEADER + _EXPECTED_DETAIL_HEADER


class TestGenerateHumanReportStartTime(unittest.TestCase):
    def test_start_time_is_shown_for_each_job_status(self) -> None:
        for status in ('success', 'failure', 'pending', 'unknown'):
            with self.subTest(status=status):
                report_data = _make_report_data(_all_pass_categories(), status=status)
                report = generate_human_report(report_data)
                self.assertIn('- **Started**: 2026-01-01T00:00:00Z\n', report)

    def test_missing_start_time_is_shown_as_unknown(self) -> None:
        for value in ('', None):
            with self.subTest(value=value):
                report_data = _make_report_data(_all_pass_categories())
                report_data['prowjob']['start_time'] = value
                report = generate_human_report(report_data)
                self.assertIn('- **Started**: unknown\n', report)

        report_data = _make_report_data(_all_pass_categories())
        del report_data['prowjob']['start_time']
        report = generate_human_report(report_data)
        self.assertIn('- **Started**: unknown\n', report)


class TestGenerateCsvReportHeader(unittest.TestCase):
    def test_header_has_45_columns_in_documented_order(self) -> None:
        report_data = _make_report_data(_all_pass_categories())
        csv_text = generate_csv_report(report_data, header=True)
        header = _parse_csv_line(csv_text.splitlines()[0])
        self.assertEqual(len(header), 45)
        self.assertEqual(header, _EXPECTED_HEADER)


class TestGenerateCsvReportRow(unittest.TestCase):
    def test_observed_suite_failures_appear_in_existing_rca_column(self) -> None:
        step = 'sandboxed-containers-operator-testsuites-osc'
        classification = {'suites': {'osc': {'step_exists': True, 'status': 'FAILURE'}}}
        summary = {'osc': {'had_junit': True, 'failing_tests': [{
            'name': 'C00100', 'test_case_number': 'C00100',
            'failure_message': 'token=secret with 2 replicas of 3 expected is not ready after 600 seconds',
        }]}}
        categories = _all_pass_categories()
        categories['osc'] = _build_suite_category({}, 'osc', step, classification, summary)
        report_data = _make_report_data(categories, status='failure', state='failure')
        rows = list(csv.reader(io.StringIO(generate_csv_report(report_data))))
        record = dict(zip(rows[0], rows[1]))

        self.assertEqual(len(rows[0]), 45)
        self.assertEqual(record['osc_root_cause'], report_data['result_categories']['osc']['root_cause'])
        self.assertIn('C00100: Deployment not ready (2/3 replicas after 600s)', record['osc_root_cause'])
        self.assertNotIn('token=secret', record['osc_root_cause'])

    def test_full_row_against_every_category_failing_fixture(self) -> None:
        report_data = _make_report_data(_every_category_failing(), status='failure', state='failure')
        csv_text = generate_csv_report(report_data, header=True)
        lines = csv_text.splitlines()
        header = _parse_csv_line(lines[0])
        row = _parse_csv_line(lines[1])
        record = dict(zip(header, row))

        self.assertEqual(record['infra_status'], 'FAILURE')
        self.assertEqual(record['infra_failed_steps'], 'ipi-install-install')
        self.assertEqual(record['infra_primary_pattern'], 'network')
        self.assertEqual(record['overall_status'], 'FAILURE')
        self.assertEqual(record['overall_failed_steps'], 'timeout')
        self.assertEqual(record['smoke_status'], 'FAILURE')
        self.assertEqual(
            record['smoke_failed_steps'], 'sandboxed-containers-operator-testsuites-gate'
        )
        self.assertEqual(record['osc_status'], 'FAILURE')
        self.assertIn('C00349', record['osc_root_cause'])
        self.assertEqual(record['kata_upstream_status'], 'FAILURE')
        self.assertIn('C00500', record['kata_upstream_root_cause'])
        self.assertEqual(record['caa_status'], 'FAILURE')
        self.assertIn('C00600', record['caa_root_cause'])

    def test_passing_job_has_blank_detail_cells_not_missing_columns(self) -> None:
        report_data = _make_report_data(_all_pass_categories())
        csv_text = generate_csv_report(report_data, header=True)
        lines = csv_text.splitlines()
        header = _parse_csv_line(lines[0])
        row = _parse_csv_line(lines[1])
        self.assertEqual(len(row), len(header))
        record = dict(zip(header, row))
        self.assertEqual(record['infra_status'], 'SUCCESS')
        self.assertEqual(record['infra_failed_steps'], '')
        self.assertEqual(record['infra_root_cause'], '')

    def test_infra_failure_cascade_renders_skipped_with_blank_details(self) -> None:
        report_data = _make_report_data(
            _infra_only_failure_categories(), status='failure', state='failure'
        )
        csv_text = generate_csv_report(report_data, header=True)
        lines = csv_text.splitlines()
        header = _parse_csv_line(lines[0])
        row = _parse_csv_line(lines[1])
        record = dict(zip(header, row))

        self.assertEqual(record['infra_status'], 'FAILURE')
        for key in ('overall', 'smoke', 'osc', 'kata_upstream', 'caa'):
            self.assertEqual(record[f'{key}_status'], 'skipped')
            self.assertEqual(record[f'{key}_time'], '')
            self.assertEqual(record[f'{key}_failed_steps'], '')
            self.assertEqual(record[f'{key}_primary_pattern'], '')
            self.assertEqual(record[f'{key}_confidence'], '')
            self.assertEqual(record[f'{key}_root_cause'], '')

    def test_failed_steps_list_is_comma_joined(self) -> None:
        categories = _all_pass_categories()
        categories['infra'] = _category(
            status='FAILURE', failed_steps=['step-a', 'step-b'],
        )
        report_data = _make_report_data(categories, status='failure', state='failure')
        csv_text = generate_csv_report(report_data, header=True)
        lines = csv_text.splitlines()
        header = _parse_csv_line(lines[0])
        row = _parse_csv_line(lines[1])
        record = dict(zip(header, row))
        self.assertEqual(record['infra_failed_steps'], 'step-a, step-b')


class TestFixedLayoutRegression(unittest.TestCase):
    """§6.2: every output format uses the same fixed layout every time."""

    def test_csv_header_and_column_count_identical_across_fixtures(self) -> None:
        fixtures = [
            _make_report_data(_all_pass_categories()),
            _make_report_data(_infra_only_failure_categories(), status='failure', state='failure'),
            _make_report_data(_every_category_failing(), status='failure', state='failure'),
        ]
        headers = [
            _parse_csv_line(generate_csv_report(rd, header=True).splitlines()[0])
            for rd in fixtures
        ]

        self.assertEqual(headers[0], headers[1])
        self.assertEqual(headers[1], headers[2])
        self.assertEqual(len(headers[0]), 45)

    def test_markdown_result_categories_section_lists_same_labels_in_same_order(self) -> None:
        fixtures = [
            _all_pass_categories(),
            _infra_only_failure_categories(),
            _every_category_failing(),
        ]
        orders = []
        for categories in fixtures:
            section = generate_result_categories_section(categories)
            # Category labels render as "**<key>**" in the order iterated.
            found = [line.split('**')[1] for line in section.splitlines() if line.startswith('- ')]
            orders.append(found)

        self.assertEqual(orders[0], ['infra', 'overall', 'smoke', 'osc', 'kata_upstream', 'caa'])
        self.assertEqual(orders[0], orders[1])
        self.assertEqual(orders[1], orders[2])

    def test_markdown_shows_skipped_status_for_infra_failure_cascade(self) -> None:
        section = generate_result_categories_section(_infra_only_failure_categories())
        self.assertIn('❌ **infra**: FAILURE', section)
        for key in ('overall', 'smoke', 'osc', 'kata_upstream', 'caa'):
            self.assertIn(f'⏭️ **{key}**: skipped', section)

    def test_build_result_categories_key_order_identical_across_fixtures(self) -> None:
        scenarios = [
            ({'state': 'success'}, 'success'),
            ({'state': 'failure'}, 'failure'),
            ({'state': 'aborted'}, 'aborted'),
        ]
        orders = []
        for prowjob_data, status in scenarios:
            categories = build_result_categories(
                prowjob_data, 'http://x', {'variant': 'unknown'}, status, '00:00', {}, {},
            )
            orders.append(list(categories.keys()))

        self.assertEqual(orders[0], orders[1])
        self.assertEqual(orders[1], orders[2])
        self.assertEqual(
            orders[0],
            ['infra', 'overall', 'smoke'] + [k.replace('-', '_') for k in TESTSUITES_SUITE_STEPS],
        )


class TestExtensibilityRegression(unittest.TestCase):
    """
    §2.1: adding a testsuites-chain suite must not require touching the
    CSV/markdown generation code -- only a TESTSUITES_SUITE_STEPS dict entry.

    Note on column *positions*: because the status block (J-O today) and the
    detail block (P onward) are each kept fully adjacent (plan §2), a new
    suite's status column is inserted at the end of the shared status block
    -- ahead of the detail block -- so every detail column's absolute index
    shifts by one slot per new suite, even though no column is renamed,
    reordered relative to its neighbors, or dropped. This test asserts the
    real, documented consequence (+6 columns total; the 6 original category
    names/relative order preserved; the new suite's own 6 columns -- one
    status slot plus its 5-column detail block -- appear only at the two
    append points, never inserted among the original columns).
    """

    def test_csv_header_grows_by_one_suite_without_renaming_or_reordering_existing_ones(
        self,
    ) -> None:
        extra_steps = dict(TESTSUITES_SUITE_STEPS)
        extra_steps['extra-suite'] = 'sandboxed-containers-operator-testsuites-extra-suite'

        with patch('lib.failure_analyzer.TESTSUITES_SUITE_STEPS', extra_steps):
            categories = build_result_categories(
                {'state': 'success'}, 'http://x', {'variant': 'unknown'}, 'success', '00:00', {}, {},
            )

        self.assertEqual(
            list(categories.keys()),
            ['infra', 'overall', 'smoke', 'osc', 'kata_upstream', 'caa', 'extra_suite'],
        )

        report_data = _make_report_data(categories)
        csv_text = generate_csv_report(report_data, header=True)
        header = _parse_csv_line(csv_text.splitlines()[0])

        self.assertEqual(len(header), 51)

        # Base (A-I) columns untouched.
        self.assertEqual(header[:9], _EXPECTED_BASE_HEADER)

        # Status block: the 6 original categories' status columns, in their
        # original relative order, plus the new suite's appended at the end
        # of that block.
        status_block = header[9:16]
        self.assertEqual(status_block, _EXPECTED_STATUS_HEADER + ['extra_suite_status'])

        # Detail block: the 6 original categories' 5-column blocks, in their
        # original relative order and internally unchanged, plus the new
        # suite's own 5-column block appended at the end.
        detail_block = header[16:]
        self.assertEqual(
            detail_block,
            _EXPECTED_DETAIL_HEADER + [
                'extra_suite_time', 'extra_suite_failed_steps', 'extra_suite_primary_pattern',
                'extra_suite_confidence', 'extra_suite_root_cause',
            ],
        )

        # No original column was renamed or reordered relative to the others.
        original_columns_present = [h for h in header if not h.startswith('extra_suite')]
        self.assertEqual(original_columns_present, _EXPECTED_HEADER)


def _gate_summary(**overrides) -> dict:
    base = {
        'suite': 'gate', 'step_name': 'sandboxed-containers-operator-testsuites-gate',
        'step_exists': True, 'status': 'SUCCESS', 'elapsed_time': '00:01',
    }
    base.update(overrides)
    return base


def _suite_summary(suite: str, **overrides) -> dict:
    base = {
        'suite': suite, 'step_name': f'sandboxed-containers-operator-testsuites-{suite}',
        'step_exists': True, 'status': 'SUCCESS', 'elapsed_time': '00:10',
        'tests': 5, 'passed': 5, 'failures': 0, 'errors': 0, 'skipped': 0,
        'had_junit': True, 'failing_tests': [], 'detected_patterns': [],
    }
    base.update(overrides)
    return base


class TestGenerateTestsuitesSection(unittest.TestCase):
    """
    generate_testsuites_section() must never render a self-skipped suite
    (disabled via TESTS_<SUITE>_ENABLE=false, or skipped because the
    testsuites-gate marker was missing) as SUCCESS, even though the step
    itself exited 0 -- see is_self_skipped_suite().
    """

    def test_real_passing_suite_renders_success(self) -> None:
        summaries = {
            'gate': _gate_summary(),
            'osc': _suite_summary('osc'),
            'kata-upstream': _suite_summary('kata-upstream'),
            'caa': _suite_summary('caa'),
        }
        section = generate_testsuites_section(summaries, None)
        self.assertIn('✅ `sandboxed-containers-operator-testsuites-caa`', section)
        self.assertIn('Status: **SUCCESS**', section)
        self.assertNotIn('Status: **skipped**', section)

    def test_disabled_suite_renders_skipped_not_success(self) -> None:
        summaries = {
            'gate': _gate_summary(),
            'osc': _suite_summary('osc'),
            'kata-upstream': _suite_summary('kata-upstream'),
            'caa': _suite_summary(
                'caa', tests=1, passed=0, failures=0, errors=0, skipped=1,
            ),
        }
        section = generate_testsuites_section(summaries, None)
        self.assertIn('⏭️ `sandboxed-containers-operator-testsuites-caa`', section)

        caa_section = section.split('`sandboxed-containers-operator-testsuites-caa`')[1]
        self.assertIn('Status: **skipped**', caa_section)
        self.assertNotIn('Status: **SUCCESS**', caa_section)

    def test_gate_step_is_never_reported_as_self_skipped(self) -> None:
        # The gate step writes no JUnit of its own -- summarize_gate() never
        # sets 'tests'/'skipped' -- so it must keep its raw SUCCESS/FAILURE
        # status untouched by the self-skip check.
        summaries = {'gate': _gate_summary(status='SUCCESS')}
        section = generate_testsuites_section(summaries, None)
        self.assertIn('✅ `sandboxed-containers-operator-testsuites-gate`', section)
        self.assertIn('Status: **SUCCESS**', section)

    def test_failing_suite_still_renders_failure(self) -> None:
        summaries = {
            'caa': _suite_summary(
                'caa', status='FAILURE', tests=1, passed=0, failures=1, errors=0, skipped=0,
            ),
        }
        section = generate_testsuites_section(summaries, None)
        self.assertIn('❌ `sandboxed-containers-operator-testsuites-caa`', section)
        self.assertIn('Status: **FAILURE**', section)

    def test_build_failure_renders_test_bug_not_failure(self) -> None:
        """
        A suite that failed because its own test code doesn't compile (zero
        JUnit, 'go_build_failure' detected) must render as TEST_BUG, never a
        plain FAILURE that looks like a product regression -- see
        is_test_code_build_failure().
        """
        summaries = {
            'osc': _suite_summary(
                'osc', status='FAILURE', tests=0, passed=0, failures=0, errors=0, skipped=0,
                had_junit=False,
                detected_patterns=[{'pattern': 'go_build_failure', 'source': 'build-log.txt'}],
                build_failure_detail='./kata_test.go:940:8: undefined: compat_otp',
            ),
        }
        section = generate_testsuites_section(summaries, None)
        self.assertIn('🐛 `sandboxed-containers-operator-testsuites-osc`', section)
        self.assertIn('Status: **TEST_BUG**', section)
        self.assertNotIn('Status: **FAILURE**', section)
        self.assertIn('Test-code build failure', section)
        self.assertIn('undefined: compat_otp', section)


class TestGenerateResultCategoriesSectionTestBug(unittest.TestCase):
    """TEST_BUG must render with its own emoji and the same detail lines FAILURE gets."""

    def test_test_bug_category_renders_detail_lines(self) -> None:
        categories = _all_pass_categories()
        categories['osc'] = _category(
            status='TEST_BUG', time='00:01',
            failed_steps='sandboxed-containers-operator-testsuites-osc',
            primary_pattern='go_build_failure', confidence='high',
            root_cause='Test code fails to compile (test-infrastructure bug, not a product defect).',
        )
        section = generate_result_categories_section(categories)
        self.assertIn('🐛 **osc**: TEST_BUG', section)
        self.assertIn('Failed step(s): `sandboxed-containers-operator-testsuites-osc`', section)
        self.assertIn('Root cause: Test code fails to compile', section)
        self.assertIn('Primary pattern: go_build_failure (confidence: high)', section)


if __name__ == '__main__':
    unittest.main()
