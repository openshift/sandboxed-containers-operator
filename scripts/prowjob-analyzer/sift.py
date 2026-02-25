#!/usr/bin/env python3
"""
Claude Prowjob Analyzer Launcher

Wrapper script to launch Claude Code with the /prowjob-analyze command.
Automatically finds the project root and invokes Claude with the correct command.

Usage:
    ./sift.py <PROW_JOB_URL>

Example:
    ./sift.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688
"""

import sys
import os
import subprocess
import argparse
from pathlib import Path


def find_git_root():
    """Find the git root directory."""
    current = Path.cwd()

    # Try from current directory upwards
    for parent in [current] + list(current.parents):
        if (parent / '.git').exists():
            return parent

    # If not found, try from script location upwards
    script_dir = Path(__file__).resolve().parent
    for parent in [script_dir] + list(script_dir.parents):
        if (parent / '.git').exists():
            return parent

    raise RuntimeError("Could not find git root directory (no .git directory found)")


def validate_prow_url(url):
    """Basic validation of Prow job URL."""
    if not url.startswith('https://prow.ci.openshift.org/view/gs/'):
        print(f"Warning: URL doesn't look like a valid Prow job URL", file=sys.stderr)
        print(f"Expected format: https://prow.ci.openshift.org/view/gs/...", file=sys.stderr)
        response = input("Continue anyway? [y/N] ")
        if response.lower() != 'y':
            sys.exit(1)


def main():
    parser = argparse.ArgumentParser(
        description='Launch Claude Code to analyze a Prow job',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
Examples:
  # Non-interactive mode (default)
  %(prog)s https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688

  # Interactive mode (opens Claude session)
  %(prog)s -i <URL>

  # With verbose output
  %(prog)s --verbose <URL>
        '''
    )

    parser.add_argument(
        'url',
        help='Prow job URL to analyze'
    )

    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Show verbose output (for debugging)'
    )

    parser.add_argument(
        '--interactive', '-i',
        action='store_true',
        help='Launch Claude in interactive mode (default: non-interactive)'
    )

    args = parser.parse_args()

    # Validate URL
    validate_prow_url(args.url)

    # Find project root
    try:
        git_root = find_git_root()
        if args.verbose:
            print(f"Found git root: {git_root}", file=sys.stderr)
    except RuntimeError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)

    # Change to project root
    os.chdir(git_root)
    if args.verbose:
        print(f"Changed to directory: {os.getcwd()}", file=sys.stderr)

    # Prepare the Claude command
    claude_command = f"/prowjob-analyze {args.url}"

    mode = "interactive" if args.interactive else "non-interactive"
    print(f"\nLaunching Claude Code in {mode} mode with command:", file=sys.stderr)
    print(f"  {claude_command}", file=sys.stderr)
    print("", file=sys.stderr)

    # Check if claude command exists
    try:
        subprocess.run(['which', 'claude'], check=True, capture_output=True)
    except subprocess.CalledProcessError:
        print("Error: 'claude' command not found in PATH", file=sys.stderr)
        print("Please install Claude Code CLI: https://claude.ai/code", file=sys.stderr)
        sys.exit(1)

    # Launch Claude with the command
    try:
        if args.interactive:
            # Interactive mode: pipe the command to claude
            cmd = f'echo "{claude_command}" | claude'

            if args.verbose:
                print(f"Executing: {cmd}", file=sys.stderr)

            # Execute the command in interactive mode
            result = subprocess.run(
                cmd,
                shell=True,
                text=True,
                cwd=git_root
            )
        else:
            # Non-interactive mode: use claude -p
            cmd = ['claude', '-p', claude_command]

            if args.verbose:
                print(f"Executing: {' '.join(cmd)}", file=sys.stderr)

            # Execute the command in non-interactive mode
            result = subprocess.run(
                cmd,
                text=True,
                cwd=git_root
            )

        sys.exit(result.returncode)

    except KeyboardInterrupt:
        print("\nInterrupted by user", file=sys.stderr)
        sys.exit(130)
    except Exception as e:
        print(f"Error launching Claude: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()
