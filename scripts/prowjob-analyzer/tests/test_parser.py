"""Unit tests for parser job status and JUnit parsing helpers."""

import unittest

from lib.parser import (
    extract_failing_testcases_from_junit,
    get_job_status,
    parse_junit_xml,
    parse_osc_suite_test_case_info,
)


def _prow(state: str) -> dict:
    return {'status': {'state': state}}


class TestGetJobStatus(unittest.TestCase):
    """Prow's own status.state is authoritative in the testsuites-chain architecture
    (no per-step override needed, unlike the retired OTP openshift-extended-test step --
    see the prowjob-analyzer plan)."""

    def test_success(self) -> None:
        self.assertEqual(get_job_status(_prow('success')), 'success')

    def test_failure(self) -> None:
        self.assertEqual(get_job_status(_prow('failure')), 'failure')

    def test_aborted(self) -> None:
        self.assertEqual(get_job_status(_prow('aborted')), 'aborted')

    def test_error(self) -> None:
        self.assertEqual(get_job_status(_prow('error')), 'error')

    def test_pending(self) -> None:
        self.assertEqual(get_job_status(_prow('pending')), 'pending')

    def test_unrecognized_state_is_unknown(self) -> None:
        self.assertEqual(get_job_status(_prow('triggered')), 'unknown')

    def test_missing_status_block_is_unknown(self) -> None:
        self.assertEqual(get_job_status({}), 'unknown')

    def test_state_is_case_insensitive(self) -> None:
        self.assertEqual(get_job_status(_prow('SUCCESS')), 'success')


class TestParseJunitXml(unittest.TestCase):
    def test_bare_testsuite_root(self) -> None:
        """OSC's own Ginkgo v2 suite writes a bare <testsuite> root (confirmed live)."""
        xml = b"""<?xml version="1.0"?>
<testsuite name="OSC Kata E2E Suite" tests="2" failures="1" errors="0" skipped="0" time="12.5">
  <testcase name="[It] [sig-kata] Kata C00367-deploy a pod [Serial]" classname="OSC Kata E2E Suite" time="10.1">
    <failure message="boom">traceback here</failure>
  </testcase>
  <testcase name="[It] [sig-kata] Kata C00100-other test [Serial]" classname="OSC Kata E2E Suite" time="2.4">
  </testcase>
</testsuite>
"""
        parsed = parse_junit_xml(xml)
        self.assertIsNotNone(parsed)
        assert parsed is not None
        self.assertEqual(parsed['tests'], 2)
        self.assertEqual(parsed['failures'], 1)
        self.assertEqual(len(parsed['testsuites']), 1)
        tcs = parsed['testsuites'][0]['testcases']
        self.assertEqual(tcs[0]['status'], 'failed')
        self.assertEqual(tcs[0]['failure_message'], 'boom')
        self.assertEqual(tcs[1]['status'], 'passed')

    def test_testsuites_wrapper_root(self) -> None:
        xml = b"""<?xml version="1.0"?>
<testsuites>
  <testsuite name="k8s-caps" tests="1" failures="0" errors="1" skipped="0" time="1.0">
    <testcase name="caps test" classname="k8s-caps" time="1.0">
      <error message="oops">stack</error>
    </testcase>
  </testsuite>
</testsuites>
"""
        parsed = parse_junit_xml(xml)
        self.assertIsNotNone(parsed)
        assert parsed is not None
        self.assertEqual(parsed['errors'], 1)
        self.assertEqual(parsed['testsuites'][0]['testcases'][0]['status'], 'error')

    def test_skipped_element(self) -> None:
        xml = b"""<testsuite name="s" tests="1" failures="0" errors="0" skipped="1">
  <testcase name="t" classname="c"><skipped/></testcase>
</testsuite>
"""
        parsed = parse_junit_xml(xml)
        assert parsed is not None
        self.assertEqual(parsed['testsuites'][0]['testcases'][0]['status'], 'skipped')

    def test_empty_content_returns_none(self) -> None:
        self.assertIsNone(parse_junit_xml(b''))

    def test_invalid_xml_returns_none(self) -> None:
        self.assertIsNone(parse_junit_xml(b'not xml'))

    def test_unexpected_root_returns_none(self) -> None:
        self.assertIsNone(parse_junit_xml(b'<unrelated/>'))


class TestExtractFailingTestcasesFromJunit(unittest.TestCase):
    def test_flattens_failed_and_error_only(self) -> None:
        junit = {
            'testsuites': [
                {
                    'name': 's1',
                    'testcases': [
                        {'name': 'a', 'status': 'passed'},
                        {'name': 'b', 'status': 'failed'},
                        {'name': 'c', 'status': 'error'},
                        {'name': 'd', 'status': 'skipped'},
                    ],
                },
            ],
        }
        out = extract_failing_testcases_from_junit(junit)
        self.assertEqual([t['name'] for t in out], ['b', 'c'])
        self.assertEqual(out[0]['suite_name'], 's1')

    def test_none_input_returns_empty(self) -> None:
        self.assertEqual(extract_failing_testcases_from_junit(None), [])


class TestParseOscSuiteTestCaseInfo(unittest.TestCase):
    def test_real_confirmed_shape(self) -> None:
        name = (
            '[It] [sig-kata] Kata C00367-deploy a pod with initContainer using '
            'kata runtime [Serial] [Serial]'
        )
        info = parse_osc_suite_test_case_info(name)
        self.assertEqual(info['test_case_number'], 'C00367')
        self.assertEqual(
            info['description'],
            'deploy a pod with initContainer using kata runtime',
        )

    def test_no_case_id_falls_back_to_full_name(self) -> None:
        info = parse_osc_suite_test_case_info('[It] some test with no case id')
        self.assertEqual(info['test_case_number'], '')
        self.assertEqual(info['description'], '[It] some test with no case id')


if __name__ == '__main__':
    unittest.main()
