# Prow Job Analyzer

A tool for analyzing OpenShift Prow job results, specifically tailored for OpenShift Sandboxed Containers (OSC) testing.

## Overview

The Prow Job Analyzer provides comprehensive analysis of Prow job runs, extracting metadata, determining pass/fail status, and identifying failure locations and root causes.

## Features

### Two-Level Analysis System

**Level 1: Overall Job Analysis** (`dig.py`):
- **Metadata Extraction**: Automatically extracts provider, OCP version, workload type, Kata RPM version, and build information
- **Status Determination**: Accurately determines if a job passed, failed, timed out, or encountered errors
- **Failed Step Detection**: Identifies which actual Prow step(s) failed by checking each step's finished.json
- **Test Summary**: Lists failing tests if tests executed
- **Multiple Output Formats**: Generates both human-readable markdown and machine-parsable JSON reports
- **In-Progress Job Handling**: Can wait for running jobs to complete before analysis
- **Local or Remote Artifacts**: Analyze from a Prow URL, from a local directory or a `.tar.gz` that matches the job artifact tree (`prowjob.json` at the job root). A tarball or directory can be used **without** a URL; report links prefer `status.url` from `prowjob.json` when present

**Level 2: Detailed Test Analysis** (`dig_failed_tests_report.py`):
- **Full Test Logs**: Extracts complete test execution logs from build-log.txt
- **Failure Summaries**: Parses "Summarizing N Failure" sections for each test
- **Test Metadata**: Reports test case ID, author, priority, elapsed time, category
- **Selective Analysis**: Can analyze specific tests by name or test all failed tests
- **Pattern Recognition**: Helps identify common failure patterns (timeouts, OOM, network issues, etc.)

### Claude Code Integration

- **Intelligent Workflow**: Automatically determines if tests ran or infrastructure failed
- **Smart Decision**: Runs detailed test analysis only when appropriate (Case A vs Case B)
- **Interactive & Non-Interactive**: Supports both quick analysis and deeper investigation modes

## Usage

### Initial Report
Find out if the tests succeeded or not
```bash
cd scripts/prowjob-analyzer
./dig.py <PROW_JOB_URL>
```
If it succeeded, there is no need to investigate further.

#### Analyzing a local artifact tree or `.tar.gz`

If you already have job artifacts (same layout as the **gsutil** command on the Prow **Artifacts** page: `prowjob.json` at the root, `artifacts/…` below), you can analyze **without** a URL:

```bash
cd scripts/prowjob-analyzer
./dig.py --artifacts /path/to/job-artifacts.tar.gz
# or an extracted directory:
./dig.py --artifacts /path/to/job-artifacts-dir
```

You can still pass a URL together with `--artifacts` if you want an explicit job identity or consistent links; otherwise links in the report use `status.url` from `prowjob.json`, or a `file://` URI for the bundle path.

To download artifacts from GCS from the tool (requires `gsutil` on `PATH`), use `--download-artifacts` with a **Prow job URL**; optional `--tar-artifacts` writes `PARENT_DIR/<build-id>.tar.gz` next to the downloaded folder. See **Options** under `dig.py` below.

#### Download, analyze, create a `.tar.gz`, and remove the directory

Yes—with a **single** `dig.py` invocation you can download, analyze, and create the tarball. The analysis step always reads the **extracted directory** (`PARENT_DIR/<build-id>/`); the `.tar.gz` is written from that tree after download (when `--tar-artifacts` is set).

```bash
cd scripts/prowjob-analyzer
./dig.py --download-artifacts /tmp --tar-artifacts '<PROW_JOB_URL>'
```

This produces:

| Path | Role |
|------|------|
| `/tmp/<build-id>/` | Full artifact tree; **used for analysis in this run** |
| `/tmp/<build-id>.tar.gz` | Same content archived for storage or later re-analysis |

`dig.py` does **not** delete the extracted directory automatically (there is no built-in “remove after tar” flag). After the command finishes successfully, if you only want to keep the archive, delete the folder yourself. The directory name is the job’s **build ID** (the last path segment of the Prow URL, or the folder you see under `PARENT_DIR`):

```bash
rm -rf "/tmp/<build-id>"
```

You can analyze the saved bundle later without re-downloading:

```bash
./dig.py --artifacts "/tmp/<build-id>.tar.gz"
```


### Further Investigation
 On failed jobs, you can use Claude (recommended) or `dig_failed_tests_report.py`\

#### Claude via script (Recommended)

The easiest way to analyze a Prow job is using the Claude launcher script from anywhere:

```bash
# Non-interactive mode (default) - get results and exit
./scripts/prowjob-analyzer/sift.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688

# Interactive mode - open Claude session for follow-up questions
./scripts/prowjob-analyzer/sift.py -i <PROW_JOB_URL>

# Or from within the prowjob-analyzer directory
cd scripts/prowjob-analyzer
./sift.py <PROW_JOB_URL>
```

The launcher script:
- Automatically finds the project root directory
- Launches Claude Code with the `/prowjob-analyze` command
- No need to manually navigate or type the command
- Runs in non-interactive mode by default (fast results)
- Use `-i` for interactive mode to ask follow-up questions

**Options:**
- `--interactive, -i`: Launch Claude in interactive mode (default: non-interactive)
- `--verbose, -v`: Show verbose output for debugging

#### In Claude Code with /prowjob-analyze

If you already have Claude Code open in the project directory:

```
/prowjob-analyze https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688
```

The analyzer supports both job URL patterns:
- **Periodic/Postsubmit**: `https://prow.ci.openshift.org/view/gs/test-platform-results/logs/{JOB_NAME}/{BUILD_ID}`
- **Presubmit/Rehearsal**: `https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/{ORG}_{REPO}/{PR}/{JOB_NAME}/{BUILD_ID}`


### Processing Konflux Workflow
Konflux will create a build and run the tests in [osc-test-catalog-integration](https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/ose-osc-tenant/applications/osc-test-catalog/integrationtests/osc-test-catalog-integration).  Each build creates a **Pipeline Run**.

Click on the one you want, go to its **Logs** and select *Download all task logs*. This will create a file called osc-test-catalog-integration-XXXX.log.  We'll call this the `konflux test log`.  It contains multiple test runs and the prowjob URLS for each that need to be fed to `dig.py`.

We will use `dig.py` to create a .csv file with all the tests and an initial RCA.  This can be imported into a spreadsheet.

```bash
cd scripts/prowjob-analyzer
echo '' > <output.csv>
URLS=$(grep ^https <konflux test log>)
for URL in $URLS
do
    echo $URL
    ./dig.py $URL --csv --no-header --no-wait >> <output.csv>
done
dos2unix <output.csv> # to conver to Unix EOL
grep -v <output.csv> > import.csv
```

Go to the *konflux* tab
Go to the last row plus 1
File -> import
Upload your import.csv file to google drive
*Import location* should be `Replace data at selected cell`
Import data.
If you have many log files, you might need to created a new spreadsheet and copy/pasted it instead.
You might need to adjust alignment, etc.  If you're doing lots of analysis/sorting/etc, you might want to make a copy and do things there.

Now you should have a list of SUCCESS/FAILURE runs.  There can be an initial RCA for failed jobs.
You can choose the prowjob URLs for further investigation

### Direct CLI Usage

You can also run the dig scripts directly:

**Level 1: Overall Analysis**
```bash
# Basic usage
./dig.py <PROW_JOB_URL>

# Local directory or tarball only (no URL)
./dig.py --artifacts /path/to/job-artifacts.tar.gz
./dig.py --artifacts /path/to/extracted-artifacts-dir

# Optional: combine URL with a local tree (reads from disk, no per-file HTTP for artifacts)
./dig.py --artifacts /path/to/dir <PROW_JOB_URL>

# Download artifacts with gsutil (requires URL), then analyze
./dig.py --download-artifacts . <PROW_JOB_URL>
./dig.py --download-artifacts /tmp --tar-artifacts <PROW_JOB_URL>

# Generate JSON output
./dig.py --json <PROW_JOB_URL> > report.json

# Verbose mode with no wait for in-progress jobs
./dig.py --verbose --no-wait <PROW_JOB_URL>

# Custom wait timeout (in seconds)
./dig.py --wait 600 <PROW_JOB_URL>
```

**Level 2: Detailed Test Analysis** (only when tests failed)
```bash
# Analyze all failed tests
./dig_failed_tests_report.py <PROW_JOB_URL>

# Analyze specific tests by name or ID
./dig_failed_tests_report.py <PROW_JOB_URL> "C00077" "C00349"

# Full test names work too
./dig_failed_tests_report.py <PROW_JOB_URL> "[sig-kata] Author:vvoronko-High-C00349-deploy peerpod with non-existing image annotation [Serial]"

# JSON output
./dig_failed_tests_report.py --json <PROW_JOB_URL> > test-report.json
```

### Options

**dig.py:**
- `url` (optional positional): Prow job URL. **Required** unless you pass `--artifacts` pointing at a directory or `.tar.gz` that contains `prowjob.json`. **Required** for `--download-artifacts`.
- `--artifacts PATH`: Local directory or `.tar.gz` of job artifacts (Prow layout: `prowjob.json` at job root). Can be used **alone** or with a URL. When set, analysis reads from disk (no per-file HTTP for artifacts).
- `--download-artifacts [PARENT_DIR]`: With a URL, run `gsutil` to copy the GCS prefix into `PARENT_DIR/<build-id>` (default parent: current directory). Requires `gsutil` on `PATH`.
- `--tar-artifacts`: With `--download-artifacts`, also write `PARENT_DIR/<build-id>.tar.gz` beside the downloaded directory.
- `--json`: Output machine-readable JSON instead of human-readable markdown
- `--csv`: Output one CSV row from the canonical report (see Konflux batch example above)
- `--no-header`: With `--csv`, omit the header row
- `--verbose, -v`: Enable verbose logging for debugging
- `--wait SECONDS`: Timeout for waiting on in-progress **remote** jobs (default: 300; use `0` or `--no-wait` to disable). Not used when analyzing only from `--artifacts` or a tree produced by `--download-artifacts`.
- `--no-wait`: Don't wait for in-progress jobs (same as `--wait 0`)

**dig_failed_tests_report.py:**
- `--json`: Output machine-readable JSON format
- `--verbose, -v`: Enable verbose logging for debugging

## Output

### Level 1: Overall Job Analysis Report (dig.py)

The default output is a comprehensive markdown report including:

- **Job Status**: Overall success/failure with emoji indicators
- **Job Overview**: Job name, build ID, duration, trigger source
- **Environment**: Provider, OCP version, workload type, Kata RPM version, build information
- **Test Results**: Total, passed, failed, and skipped test counts with categorized failure breakdown
- **Failure Analysis**: Which step(s) actually failed (e.g., `openshift-extended-test`, `ipi-install-install`)
- **Failed Tests**: List of tests that failed (grouped by category) with test IDs, authors, and priorities
- **Artifacts**: Direct links to all relevant artifacts (test results, logs, must-gather data)

### Level 2: Detailed Test Report (dig_failed_tests_report.py)

Per-test detailed analysis including:

- **Test Identification**: Test case ID (e.g., C00349), author, priority
- **Execution Details**: Elapsed time, category
- **Failure Summary**: Extracted error messages and failure reasons
- **Full Test Logs**: Complete test execution logs from start to failure
- Supports analyzing all failed tests or specific tests by name/ID

### JSON Reports

Both scripts support `--json` flag for structured output suitable for automation:

**dig.py JSON:**
```json
{
  "version": "1.0",
  "timestamp": "2025-12-16T...",
  "prowjob": {...},
  "metadata": {...},
  "test_results": {...},
  "failure_analysis": {
    "failed_steps": "openshift-extended-test",
    "failing_tests": [...]
  },
  "artifacts": {...}
}
```

**dig_failed_tests_report.py JSON:**
```json
{
  "job_url": "...",
  "tests_analyzed": 2,
  "failed_tests": [
    {
      "test_name": "...",
      "test_case_number": "C00349",
      "elapsed_time": "10m13s",
      "category": "peer_pods",
      "author": "vvoronko",
      "priority": "High",
      "failure_summary": "...",
      "full_logs": "..."
    }
  ]
}
```

## Architecture

The analyzer is built with a modular architecture:

```
prowjob-analyzer/
├── sift.py           # Claude launcher wrapper script
├── dig.py                  # Level 1: Overall job analysis
├── dig_failed_tests_report.py      # Level 2: Detailed test analysis
└── lib/                        # Analysis modules
    ├── fetcher.py             # Artifact fetching and URL parsing
    ├── parser.py              # prowjob.json and test-results.yaml parsing
    ├── metadata_extractor.py  # Metadata extraction
    ├── failure_analyzer.py    # Failure step detection and test categorization
    └── report_generator.py    # Report generation (markdown/JSON)
```

### Key Components

**User-Facing Scripts:**
1. **sift.py**: Wrapper script that launches Claude Code with the `/prowjob-analyze` command
   - Finds project root automatically
   - Supports interactive (`-i`) and non-interactive modes
   - Validates Prow URLs

2. **dig.py**: Level 1 analysis - overall job status and metadata
   - Loads `prowjob.json`, `finished.json`, `test-results.yaml`, and step logs from the Prow job URL or from a local directory / `.tar.gz` (`--artifacts`), or after `--download-artifacts`
   - Determines which step(s) failed
   - Lists failing tests (if tests ran)
   - Generates comprehensive report with metadata and artifact links

3. **dig_failed_tests_report.py**: Level 2 analysis - detailed test debugging
   - Extracts full test logs from build-log.txt
   - Parses failure summaries
   - Reports test metadata (ID, author, priority, elapsed time)
   - Only runs when tests actually executed and failed

**Library Modules:**
1. **Fetcher**: URL parsing, HTTP fetching with retries, optional local directory or `.tar.gz` artifact root (same layout as gsutil download), optional `gsutil`-based full-tree download, step detection
2. **Parser**: prowjob.json and test-results.yaml parsing, job status determination
3. **Metadata Extractor**: Provider, OCP version, workload type, Kata RPM extraction
4. **Failure Analyzer**: Failed step identification, test categorization
5. **Report Generator**: Markdown and JSON report formatting

### Claude Code Workflow

When using `/prowjob-analyze` command, Claude follows this workflow:

1. **Run dig.py** - Get overall job status and identify failed steps
2. **Examine Failure Analysis** - Check which step(s) failed
3. **Decision Logic**:
   - **Case A**: If `openshift-extended-test` failed AND tests are listed → Run `dig_failed_tests_report.py` for detailed test logs
   - **Case B**: If other step failed (e.g., `ipi-install-install`) → Tests never ran, explain infrastructure failure
   - **Case C**: If multiple steps failed → Intelligent decision based on whether tests executed
4. **Report Results** - Present analysis with artifact links and next steps

## Requirements

- Python 3.6+
- `pyyaml` library (for parsing test-results.yaml)
- `gsutil` on `PATH` (only if you use `dig.py --download-artifacts` to pull artifacts from GCS)

### Installing Dependencies

```bash
# Using pip
pip install pyyaml

# Or using system package manager (Fedora/RHEL)
sudo dnf install python3-pyyaml
```

## Exit Codes

- `0`: Job passed successfully
- `1`: Job failed, timed out, or was aborted
- `2`: Analysis error (cannot load artifacts, invalid URL, missing `prowjob.json`, CLI usage error such as neither URL nor `--artifacts`, etc.)

## Examples

### Passing Job

```bash
./dig.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1998111460479209472
```

Output:
```markdown
# Prow Job Analysis Report

## Status: ✅ SUCCESS

## Job Overview
- **Job Name**: periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods
- **Provider**: AWS
- **OCP Version**: 4.19
- **Workload**: peerpods
...
```

### Failed Periodic Job

```bash
./dig.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-azure-ipi-kata/1998111457991987200
```

Output will include failure analysis with categorized failing tests, detected patterns, and root cause analysis.

### Presubmit/Rehearsal Job

```bash
./dig.py https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/72608/rehearse-72608-periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate417-azure-ipi-coco/2001012534630420480
```

Output for rehearsal jobs includes the PR context:
```markdown
## Job Overview
- **Job Name**: rehearse-72608-periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate417-azure-ipi-coco
- **Trigger**: rehearsal
- **Provider**: Azure
- **Workload**: confidential-containers
...
```

## OSC-Specific Features

The analyzer is tailored for OSC jobs with special handling for:

- **Workload Types**: Recognizes kata, peerpods, and confidential-containers (coco) workloads
- **Kata RPM Version**: Extracts RPM version from job artifacts or identifies node-default usage
- **Catalog Information**: Extracts catalog source image, version, and build date
- **Test Categorization**: Groups failing tests by OSC-relevant categories (operator lifecycle, workload deployment, peerpods-specific, coco-specific, etc.)
- **Test Metadata Parsing**: Extracts test case IDs (C#####), authors, and priorities from test names
- **Step-Level Detection**: Identifies which actual Prow step failed (e.g., `ipi-install-install`, `sandboxed-containers-operator-create-kataconfig`, `openshift-extended-test`)
- **Special Step Handling**: Cross-checks test-results.yaml for openshift-extended-test step (which may report success in finished.json even when tests fail)
- **OSC Test Context**: Understands OSC test execution flow, setup phases, and common failure patterns

## Troubleshooting

### "Failed to fetch prowjob.json"

- **Remote mode**: Check that the Prow job URL is correct, the job has completed (or use `--wait`), and you have network access to prow.ci.openshift.org
- **Local mode** (`--artifacts`): Confirm the path is a directory or `.tar.gz` whose job root contains `prowjob.json` (if you archived a single top-level folder, the tool looks for a subdirectory that holds `prowjob.json`)

### "pyyaml not available"

Install the pyyaml library:
```bash
pip install pyyaml
```

### Job Still Running

Use the `--wait` option to wait for job completion:
```bash
./dig.py --wait 600 <URL>  # Wait up to 10 minutes
```

## Development

### Running Tests

```bash
# Test with a known passing job
./dig.py <passing-job-url>

# Test with a known failing job
./dig.py <failing-job-url>

# Test JSON output
./dig.py --json <job-url> | jq .
```

### Adding New Failure Patterns

Edit `lib/failure_analyzer.py` and add patterns to the `FAILURE_PATTERNS` dictionary:

```python
FAILURE_PATTERNS = {
    'new_pattern': [
        r'regex pattern 1',
        r'regex pattern 2',
    ],
}
```

## Contributing

When modifying the analyzer:

1. Test with both passing and failing jobs
2. Verify both human-readable and JSON output formats
3. Update this README if adding new features
4. Ensure error handling for missing artifacts

## References

- [Prow Documentation](https://docs.prow.k8s.io/)
- [OpenShift CI Documentation](https://docs.ci.openshift.org/)
- [OSC Job Definitions](https://github.com/openshift/release/tree/master/ci-operator/config/openshift/sandboxed-containers-operator)
