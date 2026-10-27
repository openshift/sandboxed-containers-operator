"""Tests for dig.py CLI and analyze_prowjob (local-only mode)."""

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


if __name__ == '__main__':
    unittest.main()
