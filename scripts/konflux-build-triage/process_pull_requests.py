#!/usr/bin/env python3
"""Gather Konflux build failure data for a list of PRs. Outputs JSON to stdout."""

import json
import re
import subprocess
import sys
from html.parser import HTMLParser

REPO = "openshift/sandboxed-containers-operator"


def gh(*args):
    result = subprocess.run(["gh"] + list(args), capture_output=True, text=True)
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def gh_json(*args):
    raw = gh(*args)
    if raw is None:
        return None
    return json.loads(raw)


def get_failed_konflux_checks(pr_number):
    checks = gh_json("pr", "checks", str(pr_number), "--repo", REPO, "--json", "name,state")
    if not checks:
        return []
    return [c for c in checks if c["name"].startswith("Red Hat Konflux") and c["state"] == "FAILURE"]


def get_check_run_details(sha):
    if not re.match(r'^[0-9a-f]{7,40}$', sha):
        return []
    raw = gh("api", f"repos/{REPO}/commits/{sha}/check-runs",
             "--jq", '.check_runs[] | select(.name | startswith("Red Hat Konflux")) '
                     '| select(.conclusion != null and .conclusion != "success" '
                     'and .conclusion != "neutral" and .conclusion != "skipped") '
                     '| {id: .id, name: .name, conclusion: .conclusion, output_text: .output.text}')
    if not raw:
        return []
    results = []
    for line in raw.strip().split("\n"):
        if line.strip():
            results.append(json.loads(line))
    return results


def get_existing_triage_comment(pr_number):
    if not isinstance(pr_number, int) or pr_number <= 0:
        return None
    raw = gh("api", f"repos/{REPO}/issues/{pr_number}/comments",
             "--jq", '.[] | select(.body | contains("<!-- konflux-triage:")) | {id: .id, body: .body}')
    if not raw:
        return None
    first_line = raw.strip().split("\n")[0]
    comment = json.loads(first_line)

    result = {"id": comment["id"], "fingerprint": None, "retest_count": 0, "retest_timestamp": None}
    body = comment["body"]

    m = re.search(r"<!-- konflux-triage:(.+?) -->", body)
    if m:
        result["fingerprint"] = m.group(1)

    m = re.search(r"<!-- konflux-retest:(.+?):(\d{4}-\d{2}-\d{2}T[\d:]+Z):(\d+) -->", body)
    if m:
        result["retest_timestamp"] = m.group(2)
        result["retest_count"] = int(m.group(3))

    return result


class TaskStatusParser(HTMLParser):
    """Parse the task status table from Konflux output_text HTML."""

    def __init__(self):
        super().__init__()
        self.tasks = []
        self._in_table = False
        self._in_row = False
        self._in_cell = False
        self._row_cells = []
        self._cell_text = ""
        self._header_row = True

    def handle_starttag(self, tag, attrs):
        if tag == "table":
            self._in_table = True
        elif tag == "tr" and self._in_table:
            self._in_row = True
            self._row_cells = []
        elif tag == "td" and self._in_row:
            self._in_cell = True
            self._cell_text = ""

    def handle_endtag(self, tag):
        if tag == "td" and self._in_cell:
            self._in_cell = False
            self._row_cells.append(self._cell_text.strip())
        elif tag == "tr" and self._in_row:
            self._in_row = False
            if self._header_row:
                self._header_row = False
            elif len(self._row_cells) >= 3:
                status_text = self._row_cells[0]
                status = "Unknown"
                if "Succeeded" in status_text:
                    status = "Succeeded"
                elif "Failed" in status_text:
                    status = "Failed"
                self.tasks.append({
                    "name": self._row_cells[2],
                    "status": status,
                    "duration": self._row_cells[1],
                })
        elif tag == "table":
            self._in_table = False

    def handle_data(self, data):
        if self._in_cell:
            self._cell_text += data


def parse_output_text(output_text):
    if not output_text:
        return {}

    result = {}

    m = re.search(r'pipelinerun/([^"<>\s]+)', output_text)
    if m:
        result["pipelinerun_name"] = m.group(1)

    m = re.search(r'task <b>(\S+)</b> has the status <b>"(\w+)"</b>', output_text)
    if m:
        result["failed_task"] = m.group(1)
        result["failure_status"] = m.group(2)

    m = re.search(r"<pre>(.*?)</pre>", output_text, re.DOTALL)
    if m:
        result["failure_snippet"] = m.group(1).strip()

    parser = TaskStatusParser()
    parser.feed(output_text)
    if parser.tasks:
        result["task_statuses"] = parser.tasks

    return result


def compute_fingerprint(check_runs):
    parts = sorted(
        f"{cr['name'].removeprefix('Red Hat Konflux / ')}({cr['id']})"
        for cr in check_runs
    )
    return ",".join(parts)


def process_pr(pr_number):
    failed = get_failed_konflux_checks(pr_number)
    if not failed:
        return None

    sha_raw = gh("pr", "view", str(pr_number), "--repo", REPO, "--json", "headRefOid", "--jq", ".headRefOid")
    if not sha_raw:
        return None

    check_runs = get_check_run_details(sha_raw)
    if not check_runs:
        return None

    failures = []
    for cr in check_runs:
        parsed = parse_output_text(cr.get("output_text", ""))
        failures.append({
            "check_run_id": cr["id"],
            "check_name": cr["name"],
            "check_conclusion": cr.get("conclusion", "failure"),
            **parsed,
        })

    fingerprint = compute_fingerprint(check_runs)
    existing_comment = get_existing_triage_comment(pr_number)

    return {
        "pr_number": pr_number,
        "sha": sha_raw,
        "failures": failures,
        "fingerprint": fingerprint,
        "existing_comment": existing_comment,
    }


def main():
    if not sys.argv[1:]:
        print("Usage: process_pull_requests.py <PR_NUMBER> [PR_NUMBER ...]", file=sys.stderr)
        sys.exit(1)

    results = []
    for pr in sys.argv[1:]:
        entry = process_pr(int(pr))
        if entry:
            results.append(entry)

    json.dump(results, sys.stdout, indent=2)
    print()


if __name__ == "__main__":
    main()
