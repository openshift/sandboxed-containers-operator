#!/usr/bin/env python3
"""
Job History Script

Extracts Prow job view URLs from a Prow job-history page -- standalone, no
local `release` clone needed (see the prowjob-analyzer plan, Workstream G1).
Useful for feeding a batch of real job URLs into dig.py/sift.py, e.g. the
Konflux-batch workflow documented in README.md.

The job-history page embeds its data as a JS variable:

    var allBuilds = [{"SpyglassLink": "/view/gs/...", "ID": "...",
        "Started": "...", "Duration": <nanoseconds>,
        "Result": "SUCCESS|FAILURE", "Refs": null}, ...];

which this script extracts directly (no headless browser needed). The page
paginates in fixed-size chunks (confirmed live: 20 at a time) via an
"Older Runs" link carrying a `?buildId=<oldest-id-shown>` cursor, which this
script follows automatically up to --limit/--all.

Usage:
    python3 job_history.py <JOB_HISTORY_URL_OR_JOB_NAME>
    python3 job_history.py <JOB_NAME> --result FAILURE --limit 50
    python3 job_history.py <JOB_NAME> --all --json > builds.json
"""

import argparse
import json
import logging
import re
import sys
from datetime import datetime, timezone
from typing import Dict, List, Optional, Tuple
from urllib.parse import urljoin
from urllib.request import Request, urlopen

logging.basicConfig(level=logging.INFO, format='%(levelname)s: %(message)s', stream=sys.stderr)
logger = logging.getLogger(__name__)

PROW_BASE = "https://prow.ci.openshift.org"
DEFAULT_BUCKET = "test-platform-results"

# Safety cap on pages fetched with --all, in case a pathological response
# ever makes the "Older Runs" link not converge (should never trigger in
# practice: 500 pages * 20/page = 10,000 builds).
_MAX_PAGES = 500

_ALL_BUILDS_RE = re.compile(r'var\s+allBuilds\s*=\s*(\[.*?\]);', re.S)
_OLDER_LINK_RE = re.compile(r'href="([^"]*\?buildId=\d+)">\s*&lt;-\s*Older Runs')
_SHOWING_RE = re.compile(r'Showing\s+(\d+)\s*/\s*(\d+)\s+results')


def build_history_url(job_or_url: str, bucket: str = DEFAULT_BUCKET) -> str:
    """
    Resolve the job-history URL to fetch.

    Accepts either a full job-history URL (used as-is, including any
    existing ``?buildId=`` cursor if the caller wants to start mid-history)
    or a bare periodic/postsubmit job name, for which the standard
    ``job-history/gs/<bucket>/logs/<job_name>`` URL is built.
    """
    if job_or_url.startswith('http://') or job_or_url.startswith('https://'):
        return job_or_url
    return f"{PROW_BASE}/job-history/gs/{bucket}/logs/{job_or_url}"


def _fetch_html(url: str) -> str:
    req = Request(url, headers={'User-Agent': 'prowjob-analyzer/1.0'})
    with urlopen(req, timeout=30) as response:
        return response.read().decode('utf-8', errors='ignore')


def _fetch_page(url: str) -> Tuple[List[Dict], Optional[str], Optional[Tuple[int, int]]]:
    """
    Fetch and parse one job-history page.

    Returns:
        ``(builds, older_page_url, (shown, total))`` -- ``older_page_url``
        is ``None`` on the last page; ``(shown, total)`` is ``None`` if the
        "Showing X/Y results" footer wasn't found.
    """
    html = _fetch_html(url)

    m = _ALL_BUILDS_RE.search(html)
    if not m:
        logger.warning(f"Could not find 'var allBuilds = [...]' on {url}")
        return [], None, None
    try:
        builds = json.loads(m.group(1))
    except json.JSONDecodeError as e:
        logger.error(f"Failed to parse allBuilds JSON from {url}: {e}")
        return [], None, None

    older_url = None
    older_m = _OLDER_LINK_RE.search(html)
    if older_m:
        older_url = urljoin(PROW_BASE, older_m.group(1).replace('&amp;', '&'))

    shown_total = None
    showing_m = _SHOWING_RE.search(html)
    if showing_m:
        shown_total = (int(showing_m.group(1)), int(showing_m.group(2)))

    return builds, older_url, shown_total


def _parse_started(value: str) -> Optional[datetime]:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace('Z', '+00:00'))
    except ValueError:
        return None


def _parse_date_arg(value: str) -> datetime:
    """Parse a --since/--until value (full ISO timestamp or bare date) as UTC."""
    v = value.strip()
    if 'T' not in v:
        v = v + 'T00:00:00+00:00'
    try:
        dt = datetime.fromisoformat(v.replace('Z', '+00:00'))
    except ValueError as e:
        raise argparse.ArgumentTypeError(f"Invalid date/time {value!r}: {e}")
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt


def _matches_filters(
    build: Dict,
    result: str,
    since: Optional[datetime],
    until: Optional[datetime],
) -> bool:
    if result != 'ALL' and (build.get('Result') or '').upper() != result:
        return False
    started = _parse_started(build.get('Started', ''))
    if since and (started is None or started < since):
        return False
    if until and (started is None or started > until):
        return False
    return True


def fetch_filtered_builds(
    start_url: str,
    result: str = 'ALL',
    since: Optional[datetime] = None,
    until: Optional[datetime] = None,
    limit: Optional[int] = 20,
    fetch_all: bool = False,
) -> List[Dict]:
    """
    Fetch builds from a job-history page, following "Older Runs" pagination
    and applying filters as pages come in.

    ``limit`` bounds the number of *filtered* results (not raw builds seen)
    so e.g. ``--result FAILURE --limit 50`` keeps paginating until 50
    failures are found or history runs out, rather than stopping after the
    first raw page. Ignored when ``fetch_all`` is set.

    Returns:
        Filtered build dicts, newest first, de-duplicated by ``ID``.
    """
    collected: List[Dict] = []
    seen_ids = set()
    url = start_url
    pages_fetched = 0

    while url:
        pages_fetched += 1
        logger.debug(f"Fetching page {pages_fetched}: {url}")
        builds, older_url, shown_total = _fetch_page(url)

        new_raw = 0
        for b in builds:
            bid = b.get('ID')
            if bid is None or bid in seen_ids:
                continue
            seen_ids.add(bid)
            new_raw += 1
            if _matches_filters(b, result, since, until):
                collected.append(b)

        if shown_total:
            logger.debug(f"Page reported {shown_total[0]}/{shown_total[1]} results")

        if not fetch_all and limit is not None and len(collected) >= limit:
            break
        if new_raw == 0:
            # No new builds on this page -- stop rather than loop forever if
            # "Older Runs" ever points back at an already-seen page.
            break
        if since and builds:
            oldest_started = _parse_started(builds[-1].get('Started', ''))
            if oldest_started and oldest_started < since:
                # Every remaining (older) page is entirely before --since.
                break
        if fetch_all and pages_fetched >= _MAX_PAGES:
            logger.warning(f"Stopping after {_MAX_PAGES} pages (safety cap)")
            break

        url = older_url

    if not fetch_all and limit is not None:
        collected = collected[:limit]
    return collected


def build_to_record(build: Dict) -> Dict:
    """Convert one raw allBuilds entry to this script's output record shape."""
    spyglass = build.get('SpyglassLink') or ''
    duration_ns = build.get('Duration') or 0
    return {
        'id': build.get('ID', ''),
        'started': build.get('Started', ''),
        'duration_seconds': int(duration_ns) // 1_000_000_000 if duration_ns else 0,
        'result': build.get('Result', ''),
        'url': urljoin(PROW_BASE, spyglass) if spyglass else '',
    }


def main():
    parser = argparse.ArgumentParser(
        description=(
            'Extract Prow job view URLs from a job-history page, standalone '
            '(no local release clone needed). Pipe the output into dig.py/sift.py.'
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
Examples:
  %(prog)s periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-kata

  %(prog)s <job-history-URL> --result FAILURE --limit 50

  %(prog)s <job-name> --since 2026-10-01 --all --json > builds.json

  # Feed straight into dig.py (matches the Konflux-batch pattern in README.md):
  for url in $(%(prog)s <job-name> --result FAILURE); do
      ./dig.py --csv --no-header --no-wait "$url" >> report.csv
  done
        ''',
    )
    parser.add_argument(
        'job_or_url',
        help='Job-history URL, or a bare periodic/postsubmit job name',
    )
    parser.add_argument(
        '--bucket', default=DEFAULT_BUCKET,
        help=f'GCS bucket to use when job_or_url is a bare job name (default: {DEFAULT_BUCKET})',
    )
    parser.add_argument(
        '--result', choices=['SUCCESS', 'FAILURE', 'ALL'], default='ALL',
        help='Filter by job result (default: ALL)',
    )
    parser.add_argument('--since', type=_parse_date_arg, metavar='DATE', help='Only builds started on/after this date/time (UTC)')
    parser.add_argument('--until', type=_parse_date_arg, metavar='DATE', help='Only builds started on/before this date/time (UTC)')
    parser.add_argument(
        '--limit', type=int, default=20, metavar='N',
        help='Stop once N filtered builds are found (default: 20, roughly one page). Ignored with --all.',
    )
    parser.add_argument(
        '--all', action='store_true',
        help='Follow "Older Runs" pagination until the full history is fetched, ignoring --limit',
    )
    parser.add_argument('--json', action='store_true', help='Output structured JSON instead of one URL per line')
    parser.add_argument('--verbose', '-v', action='store_true', help='Enable verbose logging')

    args = parser.parse_args()
    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    if args.since and args.until and args.since > args.until:
        parser.error('--since must be before --until')

    start_url = build_history_url(args.job_or_url, bucket=args.bucket)
    logger.info(f"Fetching job history from {start_url}")

    try:
        builds = fetch_filtered_builds(
            start_url,
            result=args.result,
            since=args.since,
            until=args.until,
            limit=None if args.all else args.limit,
            fetch_all=args.all,
        )
    except Exception as e:
        logger.error(f"Failed to fetch job history: {e}")
        sys.exit(2)

    if not builds:
        logger.error("No matching builds found")
        sys.exit(1)

    logger.info(f"{len(builds)} build(s) matched")

    records = [build_to_record(b) for b in builds]

    if args.json:
        print(json.dumps(records, indent=2))
    else:
        for r in records:
            print(r['url'])


if __name__ == '__main__':
    main()
