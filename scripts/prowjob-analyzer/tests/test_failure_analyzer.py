"""Unit tests for failure_analyzer helpers."""

import unittest

from lib.failure_analyzer import (
    determine_root_cause,
    detect_kata_rpm_install_context,
    extract_failed_case_ids_from_extended_build_log,
    extract_kata_rpm_dependency_detail,
)


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


if __name__ == "__main__":
    unittest.main()
