#!/usr/bin/env python3
"""
Batch Job History Script

Wraps job_history.py to pull Prow job view URLs across the whole OSC test
matrix in one shot: every combination of OCP version, cloud provider, and
workload, since a given date.

Each (version, provider, workload) combination maps to one periodic job
name:

    periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate<version>-<provider>-ipi-<workload>

e.g. ...-candidate422-azure-ipi-kata, ...-candidate421-aws-ipi-peerpods.

A job's Prow job-history page lists every run of that job name no matter
how it was triggered, so one fetch per job already covers both the
scheduled/automated periodic runs *and* the Konflux-triggered ones (Konflux
invokes the same job name via the gangway API -- see
lib/metadata_extractor.py's trigger-source detection). There's no separate
"automated" vs "konflux" endpoint to query.

Not every (version, provider, workload) combination exists on Prow (e.g. at
the time of writing there's no ARO "kata"/"coco" job, only ARO "peerpods" on
4.22, and no AWS "kata"/"coco" job at all). Missing jobs make Prow's
job-history page return an HTTP 500; this script treats that as "job doesn't
exist", logs it, and keeps going instead of aborting the whole batch.

Usage:
    python3 batch_job_history.py --since 2026-10-01
    python3 batch_job_history.py --since 2026-10-01 --versions 422 --providers azure,aro
    python3 batch_job_history.py --since 2026-10-01 --json > builds.json
    python3 batch_job_history.py --since 2026-10-01 --output-dir ./history

    # Feed straight into dig.py, same pattern as job_history.py/README.md:
    echo '' > output.csv
    for URL in $(python3 batch_job_history.py --since 2026-10-01 --no-group-headers); do
        ./dig.py "$URL" --csv --no-header --no-wait >> output.csv
    done
"""

import argparse
import json
import logging
import re
import sys
import time
from pathlib import Path
from typing import Dict, List, Optional
from urllib.error import HTTPError

from job_history import (
    DEFAULT_BUCKET,
    _parse_date_arg,
    build_history_url,
    build_to_record,
    fetch_filtered_builds,
)

logging.basicConfig(level=logging.INFO, format='%(levelname)s: %(message)s', stream=sys.stderr)
logger = logging.getLogger(__name__)

JOB_NAME_TEMPLATE = (
    'periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-'
    'candidate{version}-{provider}-ipi-{workload}'
)

DEFAULT_VERSIONS = ['420', '421', '422']
DEFAULT_PROVIDERS = ['azure', 'aws', 'aro']
DEFAULT_WORKLOADS = ['kata', 'peerpods', 'coco']

# A custom --job-template could be crafted (or a version/provider/workload
# value could sneak in) so that the formatted job name contains path
# separators, e.g. "../../etc/passwd". Since the job name is used verbatim
# to build the per-job output file path under --output-dir, that would let a
# write escape the intended directory. Job names are otherwise plain
# identifiers, so reject anything else outright.
_SAFE_JOB_NAME_RE = re.compile(r'^[A-Za-z0-9._-]+$')


def is_safe_job_name(job_name: str) -> bool:
    return bool(_SAFE_JOB_NAME_RE.match(job_name)) and '..' not in job_name


def normalize_version(value: str) -> str:
    """Normalize a version like '4.20' or '420' to the job-name form '420'."""
    v = value.strip()
    if '.' in v:
        major, minor = v.split('.', 1)
        v = f'{major}{minor}'
    return v


def split_csv(value: str) -> List[str]:
    return [p.strip() for p in value.split(',') if p.strip()]


def iter_job_names(
    versions: List[str],
    providers: List[str],
    workloads: List[str],
    template: str = JOB_NAME_TEMPLATE,
):
    """Yield (version, provider, workload, job_name) for every combination."""
    for version in versions:
        for provider in providers:
            for workload in workloads:
                job_name = template.format(version=version, provider=provider, workload=workload)
                yield version, provider, workload, job_name


def fetch_one_job(
    job_name: str,
    bucket: str,
    result: str,
    since,
    until,
) -> List[Dict]:
    """
    Fetch every filtered build for one job, following pagination until the
    full --since/--until window is covered.

    Raises on network/HTTP errors; the caller is expected to catch and skip
    the combination. Missing jobs (a job name that doesn't exist on Prow)
    surface as an ``HTTPError`` with a 404 or 500 status; any other error
    (timeouts, connection errors, unexpected HTTP statuses, ...) is a
    genuine fetch failure rather than proof the job doesn't exist, and the
    caller should report it separately.
    """
    start_url = build_history_url(job_name, bucket=bucket)
    builds = fetch_filtered_builds(
        start_url,
        result=result,
        since=since,
        until=until,
        limit=None,
        fetch_all=True,
    )
    return [build_to_record(b) for b in builds]


def write_records(records: List[Dict], path: Path, as_json: bool) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, 'w') as f:
        if as_json:
            json.dump(records, f, indent=2)
            f.write('\n')
        else:
            for r in records:
                f.write(r['url'] + '\n')


def main():
    parser = argparse.ArgumentParser(
        description=(
            'Batch-fetch Prow job view URLs across the OCP version x provider x '
            'workload matrix, wrapping job_history.py for each combination. '
            'Includes automated (periodic) and Konflux-triggered runs alike, '
            'since both appear on the same job-history page.'
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog='''
Examples:
  %(prog)s --since 2026-10-01

  %(prog)s --since 2026-10-01 --until 2026-10-20 --result FAILURE

  %(prog)s --since 2026-10-01 --versions 422 --providers azure,aro --workloads peerpods

  %(prog)s --since 2026-10-01 --json > builds.json

  %(prog)s --since 2026-10-01 --output-dir ./history
        ''',
    )
    parser.add_argument(
        '--since', type=_parse_date_arg, metavar='DATE', required=True,
        help='Only builds started on/after this date/time (UTC). Full ISO timestamp or bare date.',
    )
    parser.add_argument('--until', type=_parse_date_arg, metavar='DATE', help='Only builds started on/before this date/time (UTC)')
    parser.add_argument(
        '--versions', default=','.join(DEFAULT_VERSIONS), metavar='LIST',
        help=f'Comma-separated OCP versions, e.g. "420,421,422" or "4.20,4.21,4.22" (default: {",".join(DEFAULT_VERSIONS)})',
    )
    parser.add_argument(
        '--providers', default=','.join(DEFAULT_PROVIDERS), metavar='LIST',
        help=f'Comma-separated providers (default: {",".join(DEFAULT_PROVIDERS)})',
    )
    parser.add_argument(
        '--workloads', default=','.join(DEFAULT_WORKLOADS), metavar='LIST',
        help=f'Comma-separated workloads (default: {",".join(DEFAULT_WORKLOADS)})',
    )
    parser.add_argument(
        '--job-template', default=JOB_NAME_TEMPLATE, metavar='TEMPLATE',
        help=(
            'Job-name template with {version}/{provider}/{workload} placeholders '
            f'(default: {JOB_NAME_TEMPLATE})'
        ),
    )
    parser.add_argument(
        '--bucket', default=DEFAULT_BUCKET,
        help=f'GCS bucket for job-history URLs (default: {DEFAULT_BUCKET})',
    )
    parser.add_argument(
        '--result', choices=['SUCCESS', 'FAILURE', 'ALL'], default='ALL',
        help='Filter by job result (default: ALL -- i.e. both automated and Konflux runs, pass or fail)',
    )
    parser.add_argument(
        '--delay', type=float, default=0.0, metavar='SECONDS',
        help='Delay between per-job requests, to be polite to prow.ci.openshift.org (default: 0)',
    )
    parser.add_argument('--json', action='store_true', help='Output structured JSON instead of one URL per line')
    parser.add_argument(
        '--output-dir', metavar='DIR',
        help='Write one file per job (named <job-name>.txt or .json) under DIR, instead of only printing to stdout',
    )
    parser.add_argument(
        '--no-group-headers', action='store_true',
        help='With plain-text stdout output, omit the "# <job-name> (...)" header before each job\'s URLs',
    )
    parser.add_argument('--verbose', '-v', action='store_true', help='Enable verbose logging')

    args = parser.parse_args()
    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    if args.since and args.until and args.since > args.until:
        parser.error('--since must be before --until')

    versions = [normalize_version(v) for v in split_csv(args.versions)]
    providers = split_csv(args.providers)
    workloads = split_csv(args.workloads)

    output_dir = Path(args.output_dir) if args.output_dir else None

    all_json_records: List[Dict] = []
    found_jobs = 0
    empty_jobs = 0
    missing_jobs: List[str] = []
    failed_jobs: List[str] = []
    first = True

    for version, provider, workload, job_name in iter_job_names(versions, providers, workloads, args.job_template):
        if not first and args.delay:
            time.sleep(args.delay)
        first = False

        if not is_safe_job_name(job_name):
            logger.warning(f"Skipping {job_name!r}: not a valid job name (path separators/'..' are not allowed)")
            failed_jobs.append(job_name)
            continue

        logger.info(f"Fetching {job_name}")
        try:
            records = fetch_one_job(job_name, args.bucket, args.result, args.since, args.until)
        except HTTPError as e:
            if e.code in (404, 500):
                logger.warning(f"Skipping {job_name}: job doesn't exist on Prow ({e})")
                missing_jobs.append(job_name)
            else:
                logger.warning(f"Skipping {job_name}: fetch failed: {e}")
                failed_jobs.append(job_name)
            continue
        except Exception as e:
            logger.warning(f"Skipping {job_name}: fetch failed: {e}")
            failed_jobs.append(job_name)
            continue

        if not records:
            logger.info(f"{job_name}: no matching builds")
            empty_jobs += 1
            continue

        found_jobs += 1
        logger.info(f"{job_name}: {len(records)} build(s) matched")

        for r in records:
            r['version'] = version
            r['provider'] = provider
            r['workload'] = workload
            r['job_name'] = job_name

        if output_dir:
            ext = 'json' if args.json else 'txt'
            write_records(records, output_dir / f'{job_name}.{ext}', args.json)

        if args.json:
            all_json_records.extend(records)
        else:
            if not args.no_group_headers:
                print(f"# {job_name} ({len(records)} build(s) since {args.since.date()})")
            for r in records:
                print(r['url'])

    if args.json:
        print(json.dumps(all_json_records, indent=2))

    logger.info(
        f"Done: {found_jobs} job(s) with matches, {empty_jobs} job(s) with no matches in range, "
        f"{len(missing_jobs)} job(s) skipped (don't exist on Prow), "
        f"{len(failed_jobs)} job(s) skipped (fetch failed)"
    )
    if missing_jobs:
        logger.info("Missing jobs: " + ', '.join(missing_jobs))
    if failed_jobs:
        logger.warning("Failed jobs (fetch error, not confirmed missing): " + ', '.join(failed_jobs))

    if found_jobs == 0:
        logger.error("No matching builds found across the whole matrix")
        sys.exit(1)


if __name__ == '__main__':
    main()
