"""Tests for dig.py CLI and analyze_prowjob (local-only mode)."""

import csv
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

from dig import analyze_prowjob  # noqa: E402
from lib.fetcher import clear_artifact_cache, configure_artifact_source  # noqa: E402


_MINIMAL_PROWJOB = {
    'spec': {'job': 'periodic-ci-openshift-sandboxed-containers-operator-aws-ipi-kata'},
    'status': {
        'state': 'success',
        'build_id': 'test-build-42',
        'url': 'https://prow.ci.openshift.org/view/gs/bucket/logs/job/42',
    },
}


class TestAnalyzeProwjobLocalOnly(unittest.TestCase):
    def tearDown(self) -> None:
        configure_artifact_source(None)
        clear_artifact_cache()

    def test_directory_without_url_uses_status_url_for_base(self) -> None:
        root = tempfile.mkdtemp()
        try:
            with open(os.path.join(root, 'prowjob.json'), 'w', encoding='utf-8') as f:
                json.dump(_MINIMAL_PROWJOB, f)
            out = analyze_prowjob(url=None, artifact_source=root)
            self.assertIsNotNone(out)
            assert out is not None
            self.assertEqual(out['status'], 'success')
            self.assertEqual(
                out['base_url'],
                'https://prow.ci.openshift.org/view/gs/bucket/logs/job/42',
            )
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_tarball_without_url_uses_file_uri_when_no_status_url(self) -> None:
        root = tempfile.mkdtemp()
        try:
            pj = {
                'spec': {'job': 'periodic-ci-openshift-sandboxed-containers-operator-aws-ipi-kata'},
                'status': {'state': 'success', 'build_id': 'x'},
            }
            inner = os.path.join(root, 'job')
            os.makedirs(inner)
            with open(os.path.join(inner, 'prowjob.json'), 'w', encoding='utf-8') as f:
                json.dump(pj, f)
            tar_path = os.path.join(root, 'bundle.tar.gz')
            with tarfile.open(tar_path, 'w:gz') as tf:
                tf.add(inner, arcname='job')
            out = analyze_prowjob(url=None, artifact_source=tar_path)
            self.assertIsNotNone(out)
            assert out is not None
            self.assertTrue(out['base_url'].startswith('file://'))
            self.assertIn('bundle.tar.gz', out['base_url'].replace('%2F', '/'))
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_analyze_without_url_or_artifacts_returns_none(self) -> None:
        configure_artifact_source(None)
        out = analyze_prowjob(url=None, artifact_source=None)
        self.assertIsNone(out)


class TestDigCLI(unittest.TestCase):
    def test_cli_requires_url_or_artifacts(self) -> None:
        dig_py = os.path.join(os.path.dirname(__file__), '..', 'dig.py')
        proc = subprocess.run(
            [sys.executable, dig_py],
            capture_output=True,
            text=True,
            cwd=os.path.join(os.path.dirname(__file__), '..'),
        )
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn('artifacts', proc.stderr.lower())

    def test_kata_bats_failures_in_json_and_csv(self) -> None:
        dig_py = os.path.join(os.path.dirname(__file__), '..', 'dig.py')
        step = 'sandboxed-containers-operator-testsuites-kata-upstream'
        with tempfile.TemporaryDirectory() as root:
            prowjob = {
                'spec': {'job': 'periodic-ci-openshift-sandboxed-containers-operator-devel-'
                                'downstream-candidate-aws-ipi-kata'},
                'status': {'state': 'failure', 'build_id': '42'},
            }
            with open(os.path.join(root, 'prowjob.json'), 'w', encoding='utf-8') as f:
                json.dump(prowjob, f)

            for name in ('sandboxed-containers-operator-testsuites-gate', step):
                step_dir = os.path.join(root, 'artifacts', 'aws-ipi-kata', name)
                os.makedirs(step_dir)
                failed = name == step
                with open(os.path.join(step_dir, 'finished.json'), 'w', encoding='utf-8') as f:
                    json.dump({'result': 'FAILURE' if failed else 'SUCCESS',
                               'passed': not failed}, f)

            artifact_dir = os.path.join(root, 'artifacts', 'aws-ipi-kata', step, 'artifacts')
            os.makedirs(artifact_dir)
            with open(os.path.join(artifact_dir, 'junit_kata_upstream_k8s-caps.xml'),
                      'w', encoding='utf-8') as f:
                f.write('<testsuite tests="4" failures="1" errors="1" skipped="1">'
                        '<testcase name="Check capabilities, &quot;pod&quot;"><failure>boom</failure></testcase>'
                        '<testcase name="Config error"><error>bad</error></testcase>'
                        '<testcase name="Skipped"><skipped/></testcase>'
                        '<testcase name="Passed"/></testsuite>')
            with open(os.path.join(artifact_dir, 'junit_kata_upstream_k8s-env.xml'),
                      'w', encoding='utf-8') as f:
                f.write('<testsuite tests="1" failures="1" errors="0" skipped="0">'
                        '<testcase name="Config error"><failure>boom</failure></testcase>'
                        '</testsuite>')

            def run(fmt):
                return subprocess.run(
                    [sys.executable, dig_py, '--artifacts', root, fmt],
                    capture_output=True, text=True,
                    cwd=os.path.join(os.path.dirname(__file__), '..'),
                )

            json_proc = run('--json')
            self.assertEqual(json_proc.returncode, 1, json_proc.stderr)
            # dig.py writes a visual separator ahead of non-CSV reports.
            report = json.loads(json_proc.stdout[json_proc.stdout.index('{'):])
            category = report['result_categories']['kata_upstream']
            failed_steps = category['failed_steps']
            self.assertTrue(failed_steps.startswith(step + ' (failed tests: '))
            self.assertIn('k8s-caps.bats: Check capabilities, "pod"', failed_steps)
            self.assertIn('k8s-caps.bats: Config error', failed_steps)
            self.assertIn('k8s-env.bats: Config error', failed_steps)
            self.assertNotIn('Skipped', failed_steps)
            self.assertNotIn('Passed', failed_steps)
            self.assertNotIn('k8s-caps.bats', category['root_cause'])

            csv_proc = run('--csv')
            self.assertEqual(csv_proc.returncode, 1, csv_proc.stderr)
            rows = list(csv.reader(io.StringIO(csv_proc.stdout)))
            self.assertEqual(len(rows), 2)
            self.assertEqual(len(rows[0]), 45)
            self.assertEqual(len(rows[1]), 45)
            record = dict(zip(*rows))
            self.assertEqual(record['kata_upstream_failed_steps'], failed_steps)

    def test_caa_failures_in_json_and_csv(self) -> None:
        dig_py = os.path.join(os.path.dirname(__file__), '..', 'dig.py')
        step = 'sandboxed-containers-operator-testsuites-caa'
        with tempfile.TemporaryDirectory() as root:
            prowjob = {
                'spec': {'job': 'periodic-ci-openshift-sandboxed-containers-operator-devel-'
                                'downstream-candidate-azure-ipi-peerpods'},
                'status': {'state': 'failure', 'build_id': '42'},
            }
            with open(os.path.join(root, 'prowjob.json'), 'w', encoding='utf-8') as f:
                json.dump(prowjob, f)

            for name in ('sandboxed-containers-operator-testsuites-gate', step):
                step_dir = os.path.join(root, 'artifacts', 'azure-ipi-peerpods', name)
                os.makedirs(step_dir)
                failed = name == step
                with open(os.path.join(step_dir, 'finished.json'), 'w', encoding='utf-8') as f:
                    json.dump({'result': 'FAILURE' if failed else 'SUCCESS',
                               'passed': not failed}, f)

            artifact_dir = os.path.join(root, 'artifacts', 'azure-ipi-peerpods', step, 'artifacts')
            os.makedirs(artifact_dir)
            with open(os.path.join(artifact_dir, 'junit_caa_azure-full.xml'),
                      'w', encoding='utf-8') as f:
                f.write('<testsuite tests="4" failures="1" errors="1" skipped="1">'
                        '<testcase name="TestCreateSimplePodAzure"><failure>boom</failure></testcase>'
                        '<testcase name="TestPodOnSpecificTeeAzure/snp"><error>bad</error></testcase>'
                        '<testcase name="TestSkipped"><skipped/></testcase>'
                        '<testcase name="TestPassed"/></testsuite>')
            with open(os.path.join(artifact_dir, 'junit_caa_noresults.xml'),
                      'w', encoding='utf-8') as f:
                f.write('<testsuite tests="1" failures="1">'
                        '<testcase name="caa"><failure message="runner produced no JUnit results"/>'
                        '</testcase></testsuite>')

            def run(fmt):
                return subprocess.run(
                    [sys.executable, dig_py, '--artifacts', root, fmt],
                    capture_output=True, text=True,
                    cwd=os.path.join(os.path.dirname(__file__), '..'),
                )

            json_proc = run('--json')
            self.assertEqual(json_proc.returncode, 1, json_proc.stderr)
            report = json.loads(json_proc.stdout[json_proc.stdout.index('{'):])
            category = report['result_categories']['caa']
            failed_steps = category['failed_steps']
            self.assertEqual(
                failed_steps,
                f'{step} (failed tests: TestCreateSimplePodAzure; TestPodOnSpecificTeeAzure/snp)',
            )
            self.assertNotIn('TestCreateSimplePodAzure', category['root_cause'])

            csv_proc = run('--csv')
            self.assertEqual(csv_proc.returncode, 1, csv_proc.stderr)
            rows = list(csv.reader(io.StringIO(csv_proc.stdout)))
            self.assertEqual(len(rows), 2)
            self.assertEqual(len(rows[0]), 45)
            self.assertEqual(len(rows[1]), 45)
            self.assertEqual(dict(zip(*rows))['caa_failed_steps'], failed_steps)


if __name__ == '__main__':
    unittest.main()
