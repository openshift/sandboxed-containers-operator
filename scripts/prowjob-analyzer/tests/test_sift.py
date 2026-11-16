"""Tests for the Claude launcher and its repository-owned skill."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / 'scripts' / 'prowjob-analyzer' / 'sift.py'
URL = 'https://prow.ci.openshift.org/view/gs/test-platform-results/logs/job/123'


class TestSift(unittest.TestCase):
    def test_standard_agents_skill_alias_resolves_to_claude_definition(self):
        alias = ROOT / '.agents' / 'skills' / 'prowjob-analyzer'
        canonical = ROOT / '.claude' / 'skills' / 'prowjob-analyzer' / 'SKILL.md'
        self.assertTrue(alias.is_symlink())
        self.assertEqual((alias / 'SKILL.md').resolve(), canonical.resolve())

    def test_launcher_uses_osc_checkout_from_another_git_repo(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)
            other_repo = tmp_path / 'other'
            other_repo.mkdir()
            (other_repo / '.git').mkdir()
            bin_dir = tmp_path / 'bin'
            bin_dir.mkdir()
            claude = bin_dir / 'claude'
            claude.write_text('#!/bin/sh\nprintf "%s\\n" "$PWD" "$@"\n')
            claude.chmod(0o755)
            env = dict(os.environ, PATH=f'{bin_dir}{os.pathsep}{os.defpath}')

            result = subprocess.run(
                [sys.executable, str(SCRIPT), URL], cwd=other_repo, env=env,
                text=True, capture_output=True, check=True,
            )

        self.assertEqual(result.stdout.splitlines(), [
            str(ROOT), '-p', f'/prowjob-analyzer {URL}',
        ])

    def test_interactive_launcher_sends_skill_command(self):
        with tempfile.TemporaryDirectory() as tmp:
            bin_dir = Path(tmp) / 'bin'
            bin_dir.mkdir()
            claude = bin_dir / 'claude'
            claude.write_text('#!/bin/sh\nprintf "%s\\n" "$PWD"\ncat\n')
            claude.chmod(0o755)
            env = dict(os.environ, PATH=f'{bin_dir}{os.pathsep}{os.defpath}')

            result = subprocess.run(
                [sys.executable, str(SCRIPT), '-i', URL], cwd=tmp, env=env,
                text=True, capture_output=True, check=True,
            )

        self.assertEqual(result.stdout.splitlines(), [
            str(ROOT), f'/prowjob-analyzer {URL}',
        ])


if __name__ == '__main__':
    unittest.main()
