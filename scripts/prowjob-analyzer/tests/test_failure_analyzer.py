"""Unit tests for failure_analyzer helpers."""

import os
import shutil
import tempfile
import unittest
from unittest.mock import patch

from lib.failure_analyzer import (
    _build_suite_category,
    _observed_suite_failures,
    _observed_test_failure,
    _pending_pre_step_from_build_log,
    analyze_steps_failure,
    build_result_categories,
    check_log_for_patterns,
    classify_infra_steps,
    classify_testsuites_result,
    determine_root_cause,
    detect_kata_rpm_install_context,
    extract_failed_case_ids_from_extended_build_log,
    extract_go_build_failure_detail,
    extract_kata_rpm_dependency_detail,
    is_self_skipped_suite,
    is_test_code_build_failure,
    summarize_all_suites,
)
from lib.fetcher import TESTSUITES_SUITE_STEPS, clear_artifact_cache, configure_artifact_source


class TestOlmTrusteePatterns(unittest.TestCase):
    """FAILURE_PATTERNS categories migrated from the osc-step-debug skill (Workstream I)."""

    def test_catalogsource_not_ready(self) -> None:
        patterns = check_log_for_patterns("CatalogSource brew-catalog is not READY", "build-log.txt")
        self.assertIn('catalogsource', [p['pattern'] for p in patterns])

    def test_olm_subscription_no_installplan(self) -> None:
        patterns = check_log_for_patterns(
            "Subscription osc-operator has no InstallPlan reference", "build-log.txt"
        )
        self.assertIn('olm_subscription', [p['pattern'] for p in patterns])

    def test_kbs_auth_failure(self) -> None:
        patterns = check_log_for_patterns(
            "kbs-client: Authenticating with KBS failed: connection refused", "build-log.txt"
        )
        self.assertIn('kbs_auth', [p['pattern'] for p in patterns])

    def test_unrelated_log_matches_nothing_new(self) -> None:
        patterns = check_log_for_patterns("everything is fine here", "build-log.txt")
        names = [p['pattern'] for p in patterns]
        self.assertNotIn('catalogsource', names)
        self.assertNotIn('olm_subscription', names)
        self.assertNotIn('kbs_auth', names)


class TestExtractFailedCaseIdsFromExtendedBuildLog(unittest.TestCase):
    def test_empty_returns_empty_list(self) -> None:
        self.assertEqual(extract_failed_case_ids_from_extended_build_log(""), [])

    def test_collects_ids_on_failure_hint_lines(self) -> None:
        log = """\
• Failure [sig-foo] test -C12345- something [Serial]
panic: boom for case -C67890- extra
"""
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C12345", "C67890"],
        )

    def test_skips_lines_without_failure_context(self) -> None:
        log = """\
[sig-bar] happy path -C11111- passed
timeout: unrelated
"""
        self.assertEqual(extract_failed_case_ids_from_extended_build_log(log), [])

    def test_deduplicates_preserving_order(self) -> None:
        log = """\
error: failed test -C42424- and -C42424- again
failure: another -C77777-
"""
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C42424", "C77777"],
        )

    def test_tail_fallback_when_no_primary_hits(self) -> None:
        # ``abort`` matches tail_fail but not failure_hint (which uses ``aborted``), so
        # IDs on this line are only picked up in the tail scan when the first pass
        # found nothing.
        log = """\
[sig] quiet line with -C00001- only
abort: cleanup for -C99999- done
"""
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C99999"],
        )

    def test_summarizing_failures_line(self) -> None:
        log = "Summarizing 3 Failure: [sig] case -C50001- and -C50002- more text\n"
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C50001", "C50002"],
        )

    def test_fail_bracket_tag(self) -> None:
        log = "[Fail] [sig-foo] Kata test -C88888- description [Serial]\n"
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C88888"],
        )

    def test_primary_hits_prevent_tail_scan(self) -> None:
        # Tail fallback runs only when the first loop finds no IDs; IDs from later
        # ``abort`` lines must not be merged in once something matched earlier.
        log = """\
error: failed -C11111- first
abort: later -C99999- ignored for tail because primary already had hits
"""
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C11111"],
        )

    def test_oom_killed_hint(self) -> None:
        log = "OOMKilled pod for test -C30303- after timeout\n"
        self.assertEqual(
            extract_failed_case_ids_from_extended_build_log(log),
            ["C30303"],
        )


def _kata_rpm_install_log_snippet() -> str:
    """>=80 chars, satisfies oc debug worker + kata rpm + failed dependencies."""
    return (
        "oc debug node/ci-op-xyz-worker-eastus1-abcd -n openshift-machine-api "
        + "x" * 20
        + "\n"
        "Installing kata-containers.rpm on worker\n"
        "error: Failed dependencies:\n"
        "  libfoo is needed by kata-containers\n"
    )


class TestDetectKataRpmInstallContext(unittest.TestCase):
    def test_detects_typical_failure(self) -> None:
        self.assertTrue(detect_kata_rpm_install_context(_kata_rpm_install_log_snippet()))

    def test_false_when_text_too_short(self) -> None:
        self.assertFalse(
            detect_kata_rpm_install_context(
                "oc debug node/w-worker-1\nkata-containers.rpm\nFailed dependencies\n"
            )
        )

    def test_false_without_debug_node_worker(self) -> None:
        text = _kata_rpm_install_log_snippet().replace("worker-eastus1", "master-0")
        self.assertFalse(detect_kata_rpm_install_context(text))

    def test_false_without_kata_rpm_marker(self) -> None:
        text = _kata_rpm_install_log_snippet().replace("kata-containers.rpm", "other.rpm")
        self.assertFalse(detect_kata_rpm_install_context(text))

    def test_false_without_dependency_error(self) -> None:
        text = _kata_rpm_install_log_snippet()
        text = text.replace("Failed dependencies", "Something else")
        text = text.replace("needed by kata-containers", "needed by other")
        self.assertFalse(detect_kata_rpm_install_context(text))

    def test_accepts_rpm_uvh_kata_form(self) -> None:
        text = _kata_rpm_install_log_snippet().replace(
            "Installing kata-containers.rpm on worker",
            "rpm -Uvh /tmp/foo-kata-1.rpm",
        )
        self.assertTrue(detect_kata_rpm_install_context(text))


class TestExtractKataRpmDependencyDetail(unittest.TestCase):
    def test_empty_returns_none(self) -> None:
        self.assertIsNone(extract_kata_rpm_dependency_detail(""))

    def test_prefers_is_needed_by_line(self) -> None:
        log = """\
error: Failed dependencies:
  some-lib is needed by kata-containers
"""
        self.assertEqual(
            extract_kata_rpm_dependency_detail(log),
            "some-lib is needed by kata-containers",
        )

    def test_first_needed_by_wins(self) -> None:
        log = """\
  first-dep is needed by kata-containers
  second-dep is needed by kata-containers
"""
        self.assertEqual(
            extract_kata_rpm_dependency_detail(log),
            "first-dep is needed by kata-containers",
        )

    def test_failed_dependencies_header_when_no_needed_by_line(self) -> None:
        log = "prefix\nerror: Failed dependencies:\nmore lines\n"
        self.assertEqual(
            extract_kata_rpm_dependency_detail(log),
            "error: Failed dependencies:",
        )

    def test_returns_none_when_no_match(self) -> None:
        self.assertIsNone(
            extract_kata_rpm_dependency_detail("only unrelated log lines\n"),
        )


class TestDetermineRootCauseRpmInstall(unittest.TestCase):
    def test_high_confidence_when_composite_source_even_if_first_match_is_build_log(
        self,
    ) -> None:
        """Composite rpm_install is only added when detect_kata passed on OET prefix."""
        rc = determine_root_cause(
            [],
            [
                {'pattern': 'rpm_install', 'source': 'build-log.txt'},
                {
                    'pattern': 'rpm_install',
                    'source': 'openshift-extended-test/build-log.txt (first test), composite',
                },
            ],
            {},
            oet_build_log_first_test=None,
        )
        self.assertEqual(rc['primary_pattern'], 'rpm_install')
        self.assertEqual(rc['confidence'], 'high')
        self.assertTrue(rc.get('kata_worker_context_verified'))
        self.assertIn('Kata RPM install', rc['likely_cause'])

    def test_high_confidence_when_oet_prefix_matches_kata_worker_context(self) -> None:
        oet = _kata_rpm_install_log_snippet()
        rc = determine_root_cause(
            [],
            [
                {
                    'pattern': 'rpm_install',
                    'source': 'openshift-extended-test/build-log.txt (first test)',
                },
            ],
            {},
            oet_build_log_first_test=oet,
        )
        self.assertEqual(rc['confidence'], 'high')
        self.assertTrue(rc.get('kata_worker_context_verified'))
        self.assertIn('Kata RPM install', rc['likely_cause'])

    def test_low_confidence_for_generic_build_log_match_without_oet_verification(
        self,
    ) -> None:
        rc = determine_root_cause(
            [],
            [{'pattern': 'rpm_install', 'source': 'build-log.txt'}],
            {},
            oet_build_log_first_test=None,
        )
        self.assertEqual(rc['confidence'], 'low')
        self.assertFalse(rc.get('kata_worker_context_verified'))
        self.assertIn('not confirmed', rc['likely_cause'].lower())
        self.assertNotIn('qemu-kvm-core', ' '.join(rc.get('suggested_actions', [])))

    def test_low_confidence_when_oet_does_not_show_worker_kata_context(self) -> None:
        # Matches generic rpm_install regex (Failed dependencies) but not Kata worker path
        oet = (
            "oc debug node/ci-op-xyz-master-0-abcd -n openshift-machine-api "
            + "y" * 40
            + "\n"
            "error: Failed dependencies:\n"
            "  some-other-pkg is needed by unrelated-package\n"
        )
        rc = determine_root_cause(
            [],
            [
                {
                    'pattern': 'rpm_install',
                    'source': 'openshift-extended-test/build-log.txt (first test)',
                },
            ],
            {},
            oet_build_log_first_test=oet,
        )
        self.assertEqual(rc['confidence'], 'low')
        self.assertFalse(rc.get('kata_worker_context_verified'))


_GO_BUILD_FAILURE_LOG = (
    "==========================================\n"
    "OSC testsuites :: osc (golang e2e)\n"
    "TESTS_OSC_ENABLE=true\n"
    "==========================================\n"
    "Fetching test repo (ref devel)\n"
    "Tests commit: 80108f8a4e21ab10e0339474d66b1d1c63243902\n"
    "Running go test with timeout=120m\n"
    "go: downloading k8s.io/apimachinery v0.35.1\n"
    "# github.com/openshift/sandboxed-containers-operator/test/e2e "
    "[github.com/openshift/sandboxed-containers-operator/test/e2e.test]\n"
    "./kata_test.go:940:8: undefined: compat_otp\n"
    "./kata_test.go:1149:20: undefined: compat_otp\n"
    "./kata_test.go:1175:20: undefined: compat_otp\n"
    "FAIL\tgithub.com/openshift/sandboxed-containers-operator/test/e2e [build failed]\n"
    "ERROR: no JUnit produced at /tmp/osc-results-kuckBD/junit_osc.xml; failing the suite\n"
    "osc go test exited 1\n"
)


class TestExtractGoBuildFailureDetail(unittest.TestCase):
    def test_extracts_header_through_fail_line(self) -> None:
        detail = extract_go_build_failure_detail(_GO_BUILD_FAILURE_LOG)
        self.assertIsNotNone(detail)
        self.assertTrue(detail.startswith('# github.com/openshift/sandboxed-containers-operator/test/e2e'))
        self.assertIn('undefined: compat_otp', detail)
        self.assertTrue(detail.endswith('[build failed]'))

    def test_fail_line_alone_when_no_header(self) -> None:
        text = 'some preamble\nFAIL\tsome/pkg [build failed]\n'
        detail = extract_go_build_failure_detail(text)
        self.assertEqual(detail, 'FAIL\tsome/pkg [build failed]')

    def test_none_when_neither_marker_present(self) -> None:
        self.assertIsNone(extract_go_build_failure_detail('all good, nothing to see\n'))

    def test_none_for_empty_text(self) -> None:
        self.assertIsNone(extract_go_build_failure_detail(''))

    def test_truncates_to_max_chars(self) -> None:
        detail = extract_go_build_failure_detail(_GO_BUILD_FAILURE_LOG, max_chars=20)
        self.assertEqual(len(detail), 20)


class TestIsTestCodeBuildFailure(unittest.TestCase):
    """
    is_test_code_build_failure() must distinguish a suite that failed
    because its own test code doesn't compile (TEST_BUG) from a suite that
    failed for any other reason (a real test failure, a runtime panic,
    etc.) -- only the former should ever be reported as TEST_BUG instead of
    FAILURE.
    """

    def test_true_for_build_failure_shape(self) -> None:
        summary = {
            'had_junit': False,
            'detected_patterns': [
                {'pattern': 'go_build_failure', 'source': 'build-log.txt'},
            ],
        }
        self.assertTrue(is_test_code_build_failure(summary))

    def test_false_when_junit_was_produced(self) -> None:
        # A suite that actually ran (and wrote JUnit) never gets TEST_BUG,
        # even if 'go_build_failure' somehow also matched elsewhere in its log.
        summary = {
            'had_junit': True,
            'detected_patterns': [
                {'pattern': 'go_build_failure', 'source': 'build-log.txt'},
            ],
        }
        self.assertFalse(is_test_code_build_failure(summary))

    def test_false_without_go_build_failure_pattern(self) -> None:
        summary = {
            'had_junit': False,
            'detected_patterns': [{'pattern': 'timeout', 'source': 'build-log.txt'}],
        }
        self.assertFalse(is_test_code_build_failure(summary))

    def test_false_with_no_detected_patterns(self) -> None:
        summary = {'had_junit': False, 'detected_patterns': []}
        self.assertFalse(is_test_code_build_failure(summary))


class TestDetermineRootCauseGoBuildFailure(unittest.TestCase):
    def test_high_confidence_with_detail(self) -> None:
        rc = determine_root_cause(
            [],
            [{'pattern': 'go_build_failure', 'source': 'build-log.txt'}],
            {},
            build_failure_detail='./kata_test.go:940:8: undefined: compat_otp',
        )
        self.assertEqual(rc['primary_pattern'], 'go_build_failure')
        self.assertEqual(rc['confidence'], 'high')
        self.assertIn('not a product defect', rc['likely_cause'])
        self.assertIn('undefined: compat_otp', rc['likely_cause'])

    def test_falls_back_to_generic_message_without_detail(self) -> None:
        rc = determine_root_cause(
            [], [{'pattern': 'go_build_failure', 'source': 'build-log.txt'}], {},
        )
        self.assertEqual(rc['primary_pattern'], 'go_build_failure')
        self.assertIn('not a product defect', rc['likely_cause'])

    def test_takes_priority_over_other_patterns_in_same_log(self) -> None:
        rc = determine_root_cause(
            [],
            [
                {'pattern': 'go_build_failure', 'source': 'build-log.txt'},
                {'pattern': 'timeout', 'source': 'build-log.txt'},
            ],
            {},
        )
        self.assertEqual(rc['primary_pattern'], 'go_build_failure')


_VARIANT = 'aws-ipi-peerpods'

_ALL_TESTSUITES_CHAIN = [
    'sandboxed-containers-operator-testsuites-gate',
    'sandboxed-containers-operator-testsuites-osc',
    'sandboxed-containers-operator-testsuites-kata-upstream',
    'sandboxed-containers-operator-testsuites-caa',
]


class TestKataUpstreamFailedSteps(unittest.TestCase):
    step = 'sandboxed-containers-operator-testsuites-kata-upstream'

    def _category(self, tests, suite='kata-upstream', status='FAILURE'):
        step = f'sandboxed-containers-operator-testsuites-{suite}'
        classification = {'suites': {suite: {'step_exists': True, 'status': status}}}
        summary = {suite: {'had_junit': True, 'failing_tests': tests}}
        return _build_suite_category({}, suite, step, classification, summary)

    def test_filename_and_test_name_in_failed_steps(self):
        tests = [
            {'name': 'Check capabilities of pod', 'junit_file': 'junit_kata_upstream_k8s-caps.xml'},
            {'name': 'ConfigMap for a pod', 'junit_file': 'junit_kata_upstream_k8s-configmap.xml'},
            {'name': 'ConfigMap propagation', 'junit_file': 'junit_kata_upstream_k8s-configmap.bats.xml'},
        ]
        result = self._category(tests)
        self.assertEqual(
            result['failed_steps'],
            f'{self.step} (failed tests: k8s-caps.bats: Check capabilities of pod; '
            'k8s-configmap.bats: ConfigMap for a pod; '
            'k8s-configmap.bats: ConfigMap propagation)',
        )
        self.assertNotIn('k8s-caps.bats', result['root_cause'])

    def test_missing_unrecognized_and_synthetic_filenames(self):
        for filename in ('', 'other.xml', 'junit_kata_upstream_skip.xml',
                         'junit_kata_upstream_.xml', 'junit_kata_upstream_foo/bar.xml'):
            with self.subTest(filename=filename):
                result = self._category([{'name': 'Test name', 'junit_file': filename}])
                self.assertEqual(
                    result['failed_steps'],
                    f'{self.step} (failed tests: Test name (BATS filename unavailable))',
                )

    def test_missing_test_name_is_not_invented(self):
        result = self._category([{'junit_file': 'junit_kata_upstream_k8s-caps.xml'}])
        self.assertIn('k8s-caps.bats: <unnamed test>', result['failed_steps'])

    def test_no_results_other_suites_and_pass_are_unchanged(self):
        self.assertEqual(self._category([])['failed_steps'], self.step)
        tests = [{'name': 'test', 'junit_file': 'junit_kata_upstream_k8s-caps.xml'}]
        self.assertEqual(
            self._category(tests, suite='osc')['failed_steps'],
            'sandboxed-containers-operator-testsuites-osc',
        )
        self.assertEqual(self._category(tests, status='SUCCESS')['failed_steps'], '')


class TestCaaFailedSteps(unittest.TestCase):
    step = 'sandboxed-containers-operator-testsuites-caa'

    def _category(self, tests, status='FAILURE', **summary):
        classification = {'suites': {'caa': {'step_exists': True, 'status': status}}}
        suite_summary = {'had_junit': True, 'failing_tests': tests, **summary}
        return _build_suite_category({}, 'caa', self.step, classification, {'caa': suite_summary})

    def test_go_tests_and_subtests_are_shown(self):
        tests = [
            {'name': 'TestCreateSimplePodAzure', 'junit_file': 'junit_caa_azure-full.xml'},
            {'name': 'TestPodOnSpecificTeeAzure/snp', 'junit_file': 'junit_caa_azure-full.xml'},
        ]
        result = self._category(tests)
        self.assertEqual(
            result['failed_steps'],
            f'{self.step} (failed tests: TestCreateSimplePodAzure; TestPodOnSpecificTeeAzure/snp)',
        )
        self.assertNotIn('TestCreateSimplePodAzure', result['root_cause'])

    def test_synthetic_cases_and_empty_names_are_not_reported_as_tests(self):
        tests = [
            {'name': 'caa', 'junit_file': 'junit_caa_noresults.xml'},
            {'name': 'caa', 'junit_file': 'junit_caa_skip.xml'},
            {'name': '', 'junit_file': 'junit_caa_azure-full.xml'},
            {'name': 'TestCreateSimplePodAzure', 'junit_file': 'junit_caa_azure-full.xml'},
        ]
        self.assertEqual(
            self._category(tests)['failed_steps'],
            f'{self.step} (failed tests: TestCreateSimplePodAzure)',
        )
        self.assertEqual(self._category(tests[:3])['failed_steps'], self.step)
        self.assertEqual(self._category([])['failed_steps'], self.step)

    def test_pass_and_test_bug_do_not_invent_failed_test_names(self):
        tests = [{'name': 'TestCreateSimplePodAzure', 'junit_file': 'junit_caa_azure-full.xml'}]
        self.assertEqual(self._category(tests, status='SUCCESS')['failed_steps'], '')
        result = self._category([], had_junit=False,
                                detected_patterns=[{'pattern': 'go_build_failure',
                                                    'source': 'build-log.txt'}])
        self.assertEqual(result['status'], 'TEST_BUG')
        self.assertEqual(result['failed_steps'], self.step)


class TestObservedSuiteFailures(unittest.TestCase):
    def test_known_observations_do_not_copy_untrusted_text(self):
        tests = [
            ({'failure_message': 'secret=abc deployment x with 2 replicas of 3 '
                                 'expected is not ready after 600 seconds'},
             'Deployment not ready (2/3 replicas after 600s)'),
            ({'system_err': 'context deadline exceeded\nWaiting for containers in pod: '
                            'secret-pod\nCreateContainerError'},
             'Pod container creation did not complete before the deadline'),
            ({'system_err': 'context deadline exceeded\nWaiting for containers in pod: secret-pod'},
             'Timed out waiting for pod containers to become ready'),
            ({'system_err': 'object is being deleted: pods "secret-pod" already exists'},
             'Pod creation blocked while a previous pod was being deleted'),
            ({'system_err': 'head: /dev/kmsg: Operation not permitted'},
             'Reading /dev/kmsg was not permitted'),
            ({'failure_message': 'Deployment private-name did not get available replicas in time'},
             'Deployment did not obtain available replicas before the deadline'),
            ({'failure_message': 'context deadline exceeded, token=secret'},
             'Test reported a deadline exceeded'),
        ]
        for test, expected in tests:
            with self.subTest(expected=expected):
                self.assertEqual(_observed_test_failure(test), expected)

    def test_unrecognized_or_generic_failures_do_not_expose_text(self):
        self.assertEqual(_observed_test_failure({'failure_message': 'Failed'}), '')
        self.assertEqual(_observed_test_failure({'system_err': 'token=secret\ncontext deadline exceeded'}), '')

    def test_repeated_observations_are_deduplicated_and_case_ids_validated(self):
        detail = 'with 2 replicas of 3 expected is not ready after 600 seconds'
        tests = [
            {'test_case_number': 'C00100', 'failure_message': detail},
            {'test_case_number': 'C00100', 'failure_message': detail},
            {'test_case_number': '=secret', 'failure_message': detail},
        ]
        self.assertEqual(_observed_suite_failures(tests), [
            'C00100: Deployment not ready (2/3 replicas after 600s)',
            'Deployment not ready (2/3 replicas after 600s)',
        ])

    def test_suite_rca_includes_observation_but_not_raw_detail(self):
        step = 'sandboxed-containers-operator-testsuites-osc'
        test = {
            'name': 'C00100', 'test_case_number': 'C00100',
            'failure_message': 'token=secret with 2 replicas of 3 expected is not ready after 600 seconds',
        }
        classification = {'suites': {'osc': {'step_exists': True, 'status': 'FAILURE'}}}
        summary = {'osc': {'had_junit': True, 'failing_tests': [test]}}
        result = _build_suite_category({}, 'osc', step, classification, summary)
        self.assertIn('C00100: Deployment not ready (2/3 replicas after 600s)', result['root_cause'])
        self.assertNotIn('token=secret', result['root_cause'])

    def test_no_junit_or_recognized_pattern_still_has_a_description(self):
        step = 'sandboxed-containers-operator-testsuites-osc'
        classification = {'suites': {'osc': {'step_exists': True, 'status': 'FAILURE'}}}
        result = _build_suite_category({}, 'osc', step, classification, {'osc': {'had_junit': False}})
        self.assertEqual(result['root_cause'], 'Suite failed; inspect its JUnit and build-log artifacts.')


class TestPendingPreStepFromBuildLog(unittest.TestCase):
    @staticmethod
    def _summary(step, detail, phase='pre'):
        return (
            f'* could not run steps: step {_VARIANT} failed: "{_VARIANT}" {phase} steps failed: '
            f'"{_VARIANT}" pod "{_VARIANT}-{step}" failed: pod pending for more than 1h\n'
            f'{detail}\nLink to step on registry info site: https://example.invalid\n'
        ).encode()

    def test_detects_pending_sandbox_failure_without_copying_log(self):
        log = self._summary('ipi-conf', 'Failed to create pod sandbox: token=secret')
        with patch('lib.failure_analyzer.fetch_artifact', return_value=log):
            result = _pending_pre_step_from_build_log('http://x', _VARIANT)
        self.assertEqual(result, {
            'step': 'ipi-conf', 'pattern': 'ci_pod_sandbox',
            'message': 'CI step pod remained pending: pod sandbox creation failed.',
        })

    def test_detects_image_pull_and_manifest_unknown(self):
        log = self._summary('aws-region-override', 'ImagePullBackOff: token=secret manifest unknown')
        with patch('lib.failure_analyzer.fetch_artifact', return_value=log):
            result = _pending_pre_step_from_build_log('http://x', _VARIANT)
        self.assertEqual(result['pattern'], 'image_pull')
        self.assertEqual(result['message'],
                         'CI step pod remained pending: image pull failed. Registry reported manifest unknown.')

    def test_no_specific_reason_and_missing_or_unrelated_logs(self):
        log = self._summary('ipi-conf', 'no details')
        with patch('lib.failure_analyzer.fetch_artifact', return_value=log):
            self.assertEqual(_pending_pre_step_from_build_log('http://x', _VARIANT)['pattern'], 'ci_pod_pending')
        for log in (None, b'pod pending for more than 1h', self._summary('ipi-conf', 'detail', phase='post')):
            with self.subTest(log=log), patch('lib.failure_analyzer.fetch_artifact', return_value=log):
                self.assertIsNone(_pending_pre_step_from_build_log('http://x', _VARIANT))

    def test_blocking_failure_recovers_step_without_artifacts(self):
        info = {'step_dirs': [], 'infra_steps': [], 'infra_failed_steps': [],
                'cleanup_steps': [], 'cleanup_failed_steps': []}
        log = self._summary('ipi-conf', 'Failed to create pod sandbox: token=secret')
        classification = {'failure_kind': 'blocking_phase_failure', 'gate_step_exists': False}
        with patch('lib.failure_analyzer.classify_infra_steps', return_value=info), \
             patch('lib.failure_analyzer.fetch_artifact', return_value=log):
            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, {},
            )
        self.assertEqual(categories['infra']['status'], 'FAILURE')
        self.assertEqual(categories['infra']['failed_steps'], 'ipi-conf')
        self.assertEqual(categories['infra']['time'], 'unknown')
        self.assertEqual(categories['infra']['root_cause'],
                         'CI step pod remained pending: pod sandbox creation failed.')
        self.assertEqual(categories['overall']['status'], 'skipped')
        self.assertNotIn('token=secret', str(categories))

    def test_success_or_ran_gate_does_not_use_fallback(self):
        info = {'step_dirs': [], 'infra_steps': [], 'infra_failed_steps': [],
                'cleanup_steps': [], 'cleanup_failed_steps': []}
        classification = {'failure_kind': 'blocking_phase_failure', 'gate_step_exists': False}
        with patch('lib.failure_analyzer.classify_infra_steps', return_value=info), \
             patch('lib.failure_analyzer._pending_pre_step_from_build_log') as fallback:
            build_result_categories({'state': 'success'}, 'http://x', {'variant': _VARIANT},
                                    'success', '01:30', classification, {})
            fallback.assert_not_called()
            classification['gate_step_exists'] = True
            build_result_categories({'state': 'failure'}, 'http://x', {'variant': _VARIANT},
                                    'failure', '01:30', classification, {})
            fallback.assert_not_called()


def _write_json(path, data) -> None:
    import json
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(data, f)


def _write_text(path, text) -> None:
    with open(path, 'w', encoding='utf-8') as f:
        f.write(text)


def _step_dir(root, step) -> str:
    d = os.path.join(root, 'artifacts', _VARIANT, step)
    os.makedirs(d, exist_ok=True)
    return d


def _passing_step(root, step) -> None:
    d = _step_dir(root, step)
    _write_json(os.path.join(d, 'started.json'), {'timestamp': 1000})
    _write_json(os.path.join(d, 'finished.json'), {'timestamp': 1090, 'passed': True, 'result': 'SUCCESS'})
    _write_text(os.path.join(d, 'build-log.txt'), 'all good\n')


def _failing_step(root, step, build_log: str = 'generic failure\n') -> None:
    d = _step_dir(root, step)
    _write_json(os.path.join(d, 'started.json'), {'timestamp': 1000})
    _write_json(os.path.join(d, 'finished.json'), {'timestamp': 1090, 'passed': False, 'result': 'FAILURE'})
    _write_text(os.path.join(d, 'build-log.txt'), build_log)


def _setup_all_pass(root) -> None:
    _write_text(os.path.join(root, 'prowjob.json'), '{}')
    for step in _ALL_TESTSUITES_CHAIN:
        _passing_step(root, step)
    _passing_step(root, 'ipi-conf-aws')
    _passing_step(root, 'gather-must-gather')


class TestClassifyInfraSteps(unittest.TestCase):
    def tearDown(self) -> None:
        configure_artifact_source(None)
        clear_artifact_cache()

    def test_splits_infra_and_cleanup_and_excludes_testsuites_chain(self) -> None:
        root = tempfile.mkdtemp()
        try:
            for step in _ALL_TESTSUITES_CHAIN:
                _passing_step(root, step)
            _passing_step(root, 'ipi-conf-aws')
            _failing_step(root, 'ipi-install-install')
            _passing_step(root, 'gather-must-gather')
            _failing_step(root, 'aws-deprovision-users-and-policies')
            _write_text(os.path.join(root, 'prowjob.json'), '{}')
            configure_artifact_source(root)

            info = classify_infra_steps('http://x', _VARIANT)

            self.assertIn('ipi-conf-aws', info['infra_steps'])
            self.assertIn('ipi-install-install', info['infra_steps'])
            self.assertEqual(info['infra_failed_steps'], ['ipi-install-install'])
            self.assertIn('gather-must-gather', info['cleanup_steps'])
            self.assertIn('aws-deprovision-users-and-policies', info['cleanup_steps'])
            self.assertEqual(info['cleanup_failed_steps'], ['aws-deprovision-users-and-policies'])
            for step in _ALL_TESTSUITES_CHAIN:
                self.assertNotIn(step, info['infra_steps'])
                self.assertNotIn(step, info['cleanup_steps'])
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_empty_when_no_step_directories(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _write_text(os.path.join(root, 'prowjob.json'), '{}')
            configure_artifact_source(root)
            info = classify_infra_steps('http://x', _VARIANT)
            self.assertEqual(info['step_dirs'], [])
            self.assertEqual(info['infra_steps'], [])
            self.assertEqual(info['cleanup_steps'], [])
        finally:
            shutil.rmtree(root, ignore_errors=True)


class TestAnalyzeStepsFailure(unittest.TestCase):
    def tearDown(self) -> None:
        configure_artifact_source(None)
        clear_artifact_cache()

    def test_combines_patterns_and_log_text_across_steps(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _failing_step(root, 'step-a', build_log='connection refused\n')
            _failing_step(root, 'step-b', build_log='OOMKilled\n')
            _write_text(os.path.join(root, 'prowjob.json'), '{}')
            configure_artifact_source(root)

            result = analyze_steps_failure('http://x', _VARIANT, ['step-a', 'step-b'])

            names = [p['pattern'] for p in result['detected_patterns']]
            self.assertIn('network', names)
            self.assertIn('oom', names)
            self.assertIn('connection refused', result['build_log_text'])
            self.assertIn('OOMKilled', result['build_log_text'])
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_missing_build_log_is_skipped(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _write_text(os.path.join(root, 'prowjob.json'), '{}')
            configure_artifact_source(root)
            result = analyze_steps_failure('http://x', _VARIANT, ['nonexistent-step'])
            self.assertEqual(result['detected_patterns'], [])
            self.assertEqual(result['build_log_text'], '')
        finally:
            shutil.rmtree(root, ignore_errors=True)


class TestIsSelfSkippedSuite(unittest.TestCase):
    """
    is_self_skipped_suite() must distinguish a suite step that exited
    SUCCESS by actually running real tests from one that exited SUCCESS by
    self-skipping (disabled via TESTS_<SUITE>_ENABLE=false, or because
    ${SHARED_DIR}/testsuites_gate was missing) -- the latter must never be
    treated as a passing test.
    """

    def test_skip_shaped_junit_is_self_skipped(self) -> None:
        # Exact shape every suite's commands.sh writes for both the
        # enable-gate and the testsuites_gate skip paths:
        # tests="1" failures="0" errors="0" skipped="1".
        summary = {'had_junit': True, 'tests': 1, 'skipped': 1, 'failures': 0, 'errors': 0}
        self.assertTrue(is_self_skipped_suite(summary))

    def test_real_passing_tests_are_not_self_skipped(self) -> None:
        summary = {'had_junit': True, 'tests': 5, 'skipped': 0, 'failures': 0, 'errors': 0}
        self.assertFalse(is_self_skipped_suite(summary))

    def test_partial_skip_alongside_real_tests_is_not_self_skipped(self) -> None:
        # A real run where some individual specs were skipped (but others
        # ran) is not the same thing as the suite-level self-skip.
        summary = {'had_junit': True, 'tests': 5, 'skipped': 2, 'failures': 0, 'errors': 0}
        self.assertFalse(is_self_skipped_suite(summary))

    def test_failing_tests_are_not_self_skipped_even_if_all_marked_skipped(self) -> None:
        summary = {'had_junit': True, 'tests': 1, 'skipped': 1, 'failures': 1, 'errors': 0}
        self.assertFalse(is_self_skipped_suite(summary))

    def test_no_junit_is_not_self_skipped(self) -> None:
        summary = {'had_junit': False, 'tests': 0, 'skipped': 0, 'failures': 0, 'errors': 0}
        self.assertFalse(is_self_skipped_suite(summary))

    def test_zero_tests_is_not_self_skipped(self) -> None:
        summary = {'had_junit': True, 'tests': 0, 'skipped': 0, 'failures': 0, 'errors': 0}
        self.assertFalse(is_self_skipped_suite(summary))


class TestBuildResultCategoriesEndToEnd(unittest.TestCase):
    """build_result_categories() against synthetic classification/suite_summaries fixtures."""

    def tearDown(self) -> None:
        configure_artifact_source(None)
        clear_artifact_cache()

    def _classify_and_summarize(self, base_url: str = 'http://x', variant: str = _VARIANT):
        classification = classify_testsuites_result(base_url, variant)
        summaries = summarize_all_suites(base_url, variant, classification)
        return classification, summaries

    def test_all_pass(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'success'}, 'http://x', {'variant': _VARIANT}, 'success', '01:30',
                classification, summaries,
            )

            self.assertEqual(
                list(categories.keys()),
                ['infra', 'overall', 'smoke', 'osc', 'kata_upstream', 'caa'],
            )
            for cat in categories.values():
                self.assertEqual(cat['status'], 'SUCCESS')
                self.assertEqual(cat['failed_steps'], '')
                self.assertEqual(cat['root_cause'], '')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_infra_failure_cascades_everything_else_to_skipped(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            _failing_step(root, 'ipi-install-install', build_log='connection refused\n')
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['infra']['status'], 'FAILURE')
            self.assertIn('ipi-install-install', categories['infra']['failed_steps'])
            self.assertEqual(categories['infra']['primary_pattern'], 'network')
            # ci-operator skips the test:-phase gate (and the post:-phase
            # testsuites chain doesn't produce a meaningful result either)
            # once the pre: phase (infra) has failed -- everything else is
            # 'skipped', not independently evaluated, even though the
            # artifacts above still show smoke/osc/kata-upstream/caa as
            # SUCCESS.
            for key in ('overall', 'smoke', 'osc', 'kata_upstream', 'caa'):
                self.assertEqual(categories[key]['status'], 'skipped')
                self.assertEqual(categories[key]['time'], '')
                self.assertEqual(categories[key]['failed_steps'], '')
                self.assertEqual(categories[key]['primary_pattern'], '')
                self.assertEqual(categories[key]['confidence'], '')
                self.assertEqual(categories[key]['root_cause'], '')
            self.assertEqual(
                list(categories.keys()),
                ['infra', 'overall', 'smoke', 'osc', 'kata_upstream', 'caa'],
            )
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_smoke_only_failure(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            _failing_step(
                root, 'sandboxed-containers-operator-testsuites-gate',
                build_log='timeout waiting for marker\n',
            )
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['smoke']['status'], 'FAILURE')
            self.assertEqual(
                categories['smoke']['failed_steps'],
                'sandboxed-containers-operator-testsuites-gate',
            )
            self.assertEqual(categories['smoke']['primary_pattern'], 'timeout')
            # Status independence (plan §4): other categories still report
            # their own real outcome even though the gate (blocking phase)
            # failed.
            for key in ('infra', 'osc', 'kata_upstream', 'caa'):
                self.assertEqual(categories[key]['status'], 'SUCCESS')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_one_suite_failure(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            step = 'sandboxed-containers-operator-testsuites-osc'
            _failing_step(root, step)
            artifacts_dir = os.path.join(root, 'artifacts', _VARIANT, step, 'artifacts')
            os.makedirs(artifacts_dir, exist_ok=True)
            junit = (
                '<testsuite name="osc" tests="1" failures="1" errors="0" skipped="0" time="12.5">\n'
                '  <testcase name="[It] [sig-kata] Kata C00349-some test [Serial]" time="12.5">\n'
                '    <failure message="secret=abc with 2 replicas of 3 expected '
                'is not ready after 600 seconds">panic: boom</failure>\n'
                '  </testcase>\n'
                '</testsuite>\n'
            )
            _write_text(os.path.join(artifacts_dir, 'junit_osc.xml'), junit)
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['osc']['status'], 'FAILURE')
            self.assertEqual(categories['osc']['failed_steps'], step)
            self.assertIn('C00349', categories['osc']['root_cause'])
            self.assertIn('Deployment not ready (2/3 replicas after 600s)', categories['osc']['root_cause'])
            self.assertNotIn('secret=abc', categories['osc']['root_cause'])
            for key in ('infra', 'smoke', 'kata_upstream', 'caa'):
                self.assertEqual(categories[key]['status'], 'SUCCESS')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_suite_build_failure_reports_test_bug_and_overall_is_success_when_a_sibling_really_ran(
        self,
    ) -> None:
        """
        A suite whose test code fails to compile (go test's own "FAIL <pkg>
        [build failed]", zero JUnit) must be reported as TEST_BUG, not
        FAILURE. A TEST_BUG suite doesn't count as a real failure for
        'overall's own determination -- but 'overall' must not become
        'skipped' either unless *every* suite in the chain is skipped/
        TEST_BUG: here kata-upstream and caa both genuinely ran and passed
        (per _setup_all_pass), so that real signal means the job isn't
        inconclusive -- 'overall' is SUCCESS, with blank detail fields (see
        build_result_categories()).
        """
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            step = 'sandboxed-containers-operator-testsuites-osc'
            _failing_step(root, step, build_log=_GO_BUILD_FAILURE_LOG)
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['osc']['status'], 'TEST_BUG')
            self.assertEqual(categories['osc']['failed_steps'], step)
            self.assertIn('undefined: compat_otp', categories['osc']['root_cause'])
            self.assertEqual(categories['osc']['primary_pattern'], 'go_build_failure')

            self.assertEqual(categories['overall']['status'], 'SUCCESS')
            self.assertEqual(categories['overall']['failed_steps'], '')
            self.assertEqual(categories['overall']['primary_pattern'], '')
            self.assertEqual(categories['overall']['confidence'], '')
            self.assertEqual(categories['overall']['root_cause'], '')

            for key in ('infra', 'smoke', 'kata_upstream', 'caa'):
                self.assertEqual(categories[key]['status'], 'SUCCESS')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_overall_is_skipped_only_when_every_suite_is_skipped_or_test_bug(self) -> None:
        """
        'overall' is 'skipped' only when *every* testsuites-chain suite
        produced no real result at all (TEST_BUG or self-skipped) -- not
        merely because the one suite that raw-FAILUREd was a test bug. This
        is the companion case to the test above: here kata-upstream and caa
        are both self-skipped (disabled), so there is no real result
        anywhere in the chain, and 'overall' is 'skipped' too.
        """
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            osc_step = 'sandboxed-containers-operator-testsuites-osc'
            _failing_step(root, osc_step, build_log=_GO_BUILD_FAILURE_LOG)

            for suite, step in (
                ('kata-upstream', 'sandboxed-containers-operator-testsuites-kata-upstream'),
                ('caa', 'sandboxed-containers-operator-testsuites-caa'),
            ):
                artifacts_dir = os.path.join(root, 'artifacts', _VARIANT, step, 'artifacts')
                os.makedirs(artifacts_dir, exist_ok=True)
                # Exact skip-shape every suite's commands.sh writes when disabled.
                junit = (
                    f'<testsuite name="{suite}" tests="1" failures="0" errors="0" skipped="1">\n'
                    f'  <testcase name="{suite}" classname="osc.testsuites.{suite}" time="0">\n'
                    '    <skipped message="disabled"/>\n'
                    '  </testcase>\n'
                    '</testsuite>\n'
                )
                fname = f"junit_{suite.replace('-', '_')}_skip.xml"
                _write_text(os.path.join(artifacts_dir, fname), junit)

            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['osc']['status'], 'TEST_BUG')
            self.assertEqual(categories['kata_upstream']['status'], 'skipped')
            self.assertEqual(categories['caa']['status'], 'skipped')

            self.assertEqual(categories['overall']['status'], 'skipped')
            self.assertEqual(categories['overall']['failed_steps'], '')
            self.assertEqual(categories['overall']['primary_pattern'], '')
            self.assertEqual(categories['overall']['confidence'], '')
            self.assertEqual(categories['overall']['root_cause'], '')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_suite_build_failure_does_not_mask_a_real_failure_in_a_sibling_suite(self) -> None:
        """
        If a sibling suite genuinely failed (a real test assertion failure,
        not a build failure) alongside a TEST_BUG suite, 'overall' must stay
        FAILURE -- the downgrade only applies when *every* testsuites-chain
        suite contributing to the job's FAILURE state is itself a TEST_BUG.
        """
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            osc_step = 'sandboxed-containers-operator-testsuites-osc'
            _failing_step(root, osc_step, build_log=_GO_BUILD_FAILURE_LOG)

            kata_upstream_step = 'sandboxed-containers-operator-testsuites-kata-upstream'
            _failing_step(root, kata_upstream_step)
            artifacts_dir = os.path.join(root, 'artifacts', _VARIANT, kata_upstream_step, 'artifacts')
            os.makedirs(artifacts_dir, exist_ok=True)
            junit = (
                '<testsuite name="k8s-caps" tests="1" failures="1" errors="0" skipped="0">\n'
                '  <testcase name="k8s-caps-test" time="1.0">\n'
                '    <failure message="boom">real assertion failure</failure>\n'
                '  </testcase>\n'
                '</testsuite>\n'
            )
            _write_text(os.path.join(artifacts_dir, 'junit_kata_upstream_k8s-caps.xml'), junit)

            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['osc']['status'], 'TEST_BUG')
            self.assertEqual(categories['kata_upstream']['status'], 'FAILURE')
            self.assertEqual(categories['overall']['status'], 'FAILURE')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_self_skipped_suite_reports_skipped_not_success(self) -> None:
        """
        A suite disabled via TESTS_<SUITE>_ENABLE=false (or self-skipped
        because the gate marker was missing) exits 0 -- its finished.json
        says SUCCESS -- but must never be reported as a passing test:
        build_result_categories() must report it as 'skipped'.
        """
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            step = 'sandboxed-containers-operator-testsuites-caa'
            artifacts_dir = os.path.join(root, 'artifacts', _VARIANT, step, 'artifacts')
            os.makedirs(artifacts_dir, exist_ok=True)
            # Exact shape caa's commands.sh writes when self-skipping.
            junit = (
                '<testsuite name="caa" tests="1" failures="0" errors="0" skipped="1">\n'
                '  <testcase name="caa" classname="osc.testsuites.caa" time="0">\n'
                '    <skipped message="TESTS_CAA_ENABLE=false"/>\n'
                '  </testcase>\n'
                '</testsuite>\n'
            )
            _write_text(os.path.join(artifacts_dir, 'junit_caa_skip.xml'), junit)
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'success'}, 'http://x', {'variant': _VARIANT}, 'success', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['caa']['status'], 'skipped')
            self.assertEqual(categories['caa']['failed_steps'], '')
            self.assertEqual(categories['caa']['root_cause'], '')
            # Every other category (including the gate, which has no JUnit
            # of its own and genuinely did run) is unaffected.
            for key in ('infra', 'overall', 'smoke', 'osc', 'kata_upstream'):
                self.assertEqual(categories[key]['status'], 'SUCCESS')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_cleanup_step_only_failure_feeds_overall(self) -> None:
        root = tempfile.mkdtemp()
        try:
            _setup_all_pass(root)
            _failing_step(root, 'gather-must-gather', build_log='OOMKilled while gathering\n')
            configure_artifact_source(root)
            classification, summaries = self._classify_and_summarize()

            categories = build_result_categories(
                {'state': 'failure'}, 'http://x', {'variant': _VARIANT}, 'failure', '01:30',
                classification, summaries,
            )

            self.assertEqual(categories['infra']['status'], 'SUCCESS')
            self.assertEqual(categories['overall']['status'], 'FAILURE')
            self.assertIn('gather-must-gather', categories['overall']['failed_steps'])
            self.assertEqual(categories['overall']['primary_pattern'], 'oom')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_overall_timeout_state(self) -> None:
        categories = build_result_categories(
            {'state': 'timeout'}, 'http://x', {'variant': 'unknown'}, 'failure', '05:00', {}, {},
        )
        self.assertEqual(categories['overall']['failed_steps'], 'timeout')
        self.assertEqual(categories['overall']['root_cause'], 'The job exceeded its execution timeout.')

    def test_overall_aborted_state(self) -> None:
        categories = build_result_categories(
            {'state': 'aborted'}, 'http://x', {'variant': 'unknown'}, 'aborted', '05:00', {}, {},
        )
        self.assertEqual(categories['overall']['failed_steps'], 'aborted')
        self.assertEqual(categories['overall']['root_cause'], 'The job was aborted.')

    def test_overall_error_state(self) -> None:
        categories = build_result_categories(
            {'state': 'error'}, 'http://x', {'variant': 'unknown'}, 'error', '05:00', {}, {},
        )
        self.assertEqual(categories['overall']['failed_steps'], 'infrastructure')
        self.assertEqual(
            categories['overall']['root_cause'], 'The job failed due to infrastructure issues.'
        )

    def test_variant_unknown_everything_not_run_except_overall(self) -> None:
        categories = build_result_categories(
            {'state': 'failure'}, 'http://x', {'variant': 'unknown'}, 'failure', '01:30', {}, {},
        )
        self.assertEqual(categories['infra']['status'], 'not run')
        self.assertEqual(categories['smoke']['status'], 'not run')
        for suite_key in ('osc', 'kata_upstream', 'caa'):
            self.assertEqual(categories[suite_key]['status'], 'not run')
        self.assertEqual(categories['overall']['status'], 'FAILURE')
        self.assertEqual(categories['overall']['root_cause'], 'unknown')

    def test_key_order_is_fixed_and_matches_testsuites_suite_steps(self) -> None:
        categories = build_result_categories(
            {'state': 'success'}, 'http://x', {'variant': 'unknown'}, 'success', '00:00', {}, {},
        )
        self.assertEqual(
            list(categories.keys()),
            ['infra', 'overall', 'smoke'] + [k.replace('-', '_') for k in TESTSUITES_SUITE_STEPS],
        )


class TestBuildResultCategoriesExtensibility(unittest.TestCase):
    """Proves §2.1's "adding a suite is a one-line config change" claim."""

    def test_new_suite_is_appended_without_code_changes(self) -> None:
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
        self.assertEqual(categories['extra_suite']['status'], 'not run')


if __name__ == "__main__":
    unittest.main()
