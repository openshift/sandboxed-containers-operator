# Prow Job Analyzer

A tool for analyzing OpenShift Prow job results, specifically tailored for OpenShift Sandboxed Containers (OSC) testing.

## Overview

The Prow Job Analyzer provides comprehensive analysis of Prow job runs, extracting metadata, determining pass/fail status, and identifying failure locations and root causes.

## Features

### Two-Level Analysis System

**Level 1: Overall Job Analysis** (`analyze.py`):
- **Metadata Extraction**: Automatically extracts provider, OCP version, workload type, Kata RPM version, and build information
- **Status Determination**: Accurately determines if a job passed, failed, timed out, or encountered errors
- **Failed Step Detection**: Identifies which actual Prow step(s) failed by checking each step's finished.json
- **Test Summary**: Lists failing tests if tests executed
- **Multiple Output Formats**: Generates both human-readable markdown and machine-parsable JSON reports
- **In-Progress Job Handling**: Can wait for running jobs to complete before analysis

**Level 2: Detailed Test Analysis** (`failed_tests_report.py`):
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

### Quick Start: Claude Launcher (Recommended)

The easiest way to analyze a Prow job is using the Claude launcher script from anywhere:

```bash
# Non-interactive mode (default) - get results and exit
./scripts/prowjob-analyzer/analyze-claude.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688

# Interactive mode - open Claude session for follow-up questions
./scripts/prowjob-analyzer/analyze-claude.py -i <PROW_JOB_URL>

# Or from within the prowjob-analyzer directory
cd scripts/prowjob-analyzer
./analyze-claude.py <PROW_JOB_URL>
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

### Via Claude Code Slash Command

If you already have Claude Code open in the project directory:

```
/prowjob-analyze https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688
```

The analyzer supports both job URL patterns:
- **Periodic/Postsubmit**: `https://prow.ci.openshift.org/view/gs/test-platform-results/logs/{JOB_NAME}/{BUILD_ID}`
- **Presubmit/Rehearsal**: `https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/{ORG}_{REPO}/{PR}/{JOB_NAME}/{BUILD_ID}`

### Direct CLI Usage

You can also run the analyzer scripts directly:

**Level 1: Overall Analysis**
```bash
# Basic usage
./analyze.py <PROW_JOB_URL>

# Generate JSON output
./analyze.py --json <PROW_JOB_URL> > report.json

# Verbose mode with no wait for in-progress jobs
./analyze.py --verbose --no-wait <PROW_JOB_URL>

# Custom wait timeout (in seconds)
./analyze.py --wait 600 <PROW_JOB_URL>
```

**Level 2: Detailed Test Analysis** (only when tests failed)
```bash
# Analyze all failed tests
./failed_tests_report.py <PROW_JOB_URL>

# Analyze specific tests by name or ID
./failed_tests_report.py <PROW_JOB_URL> "C00077" "C00349"

# Full test names work too
./failed_tests_report.py <PROW_JOB_URL> "[sig-kata] Author:vvoronko-High-C00349-deploy peerpod with non-existing image annotation [Serial]"

# JSON output
./failed_tests_report.py --json <PROW_JOB_URL> > test-report.json
```

### Options

**analyze.py:**
- `--json`: Output machine-readable JSON format instead of human-readable markdown
- `--verbose, -v`: Enable verbose logging for debugging
- `--wait SECONDS`: Set timeout for waiting for in-progress jobs (default: 300 seconds)
- `--no-wait`: Don't wait for in-progress jobs (analyze immediately)

**failed_tests_report.py:**
- `--json`: Output machine-readable JSON format
- `--verbose, -v`: Enable verbose logging for debugging

## Output

### Level 1: Overall Job Analysis Report (analyze.py)

The default output is a comprehensive markdown report including:

- **Job Status**: Overall success/failure with emoji indicators
- **Job Overview**: Job name, build ID, duration, trigger source
- **Environment**: Provider, OCP version, workload type, Kata RPM version, build information
- **Test Results**: Total, passed, failed, and skipped test counts with categorized failure breakdown
- **Failure Analysis**: Which step(s) actually failed (e.g., `openshift-extended-test`, `ipi-install-install`)
- **Failed Tests**: List of tests that failed (grouped by category) with test IDs, authors, and priorities
- **Artifacts**: Direct links to all relevant artifacts (test results, logs, must-gather data)

### Level 2: Detailed Test Report (failed_tests_report.py)

Per-test detailed analysis including:

- **Test Identification**: Test case ID (e.g., C00349), author, priority
- **Execution Details**: Elapsed time, category
- **Failure Summary**: Extracted error messages and failure reasons
- **Full Test Logs**: Complete test execution logs from start to failure
- Supports analyzing all failed tests or specific tests by name/ID

### JSON Reports

Both scripts support `--json` flag for structured output suitable for automation:

**analyze.py JSON:**
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

**failed_tests_report.py JSON:**
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
├── analyze-claude.py           # Claude launcher wrapper script
├── analyze.py                  # Level 1: Overall job analysis
├── failed_tests_report.py      # Level 2: Detailed test analysis
└── lib/                        # Analysis modules
    ├── fetcher.py             # Artifact fetching and URL parsing
    ├── parser.py              # prowjob.json and test-results.yaml parsing
    ├── metadata_extractor.py  # Metadata extraction
    ├── failure_analyzer.py    # Failure step detection and test categorization
    └── report_generator.py    # Report generation (markdown/JSON)
```

### Key Components

**User-Facing Scripts:**
1. **analyze-claude.py**: Wrapper script that launches Claude Code with the `/prowjob-analyze` command
   - Finds project root automatically
   - Supports interactive (`-i`) and non-interactive modes
   - Validates Prow URLs

2. **analyze.py**: Level 1 analysis - overall job status and metadata
   - Fetches prowjob.json, finished.json, test-results.yaml
   - Determines which step(s) failed
   - Lists failing tests (if tests ran)
   - Generates comprehensive report with metadata and artifact links

3. **failed_tests_report.py**: Level 2 analysis - detailed test debugging
   - Extracts full test logs from build-log.txt
   - Parses failure summaries
   - Reports test metadata (ID, author, priority, elapsed time)
   - Only runs when tests actually executed and failed

**Library Modules:**
1. **Fetcher**: URL parsing, artifact downloading with retry logic, step detection
2. **Parser**: prowjob.json and test-results.yaml parsing, job status determination
3. **Metadata Extractor**: Provider, OCP version, workload type, Kata RPM extraction
4. **Failure Analyzer**: Failed step identification, test categorization
5. **Report Generator**: Markdown and JSON report formatting

### Claude Code Workflow

When using `/prowjob-analyze` command, Claude follows this workflow:

1. **Run analyze.py** - Get overall job status and identify failed steps
2. **Examine Failure Analysis** - Check which step(s) failed
3. **Decision Logic**:
   - **Case A**: If `openshift-extended-test` failed AND tests are listed → Run `failed_tests_report.py` for detailed test logs
   - **Case B**: If other step failed (e.g., `ipi-install-install`) → Tests never ran, explain infrastructure failure
   - **Case C**: If multiple steps failed → Intelligent decision based on whether tests executed
4. **Report Results** - Present analysis with artifact links and next steps

## Requirements

- Python 3.6+
- `pyyaml` library (for parsing test-results.yaml)

### Installing Dependencies

```bash
# From the analyzer directory
pip install -r requirements.txt

# Or, from the repo root
pip install -r scripts/prowjob-analyzer/requirements.txt

# Direct install
pip install pyyaml

# Or using system package manager (Fedora/RHEL)
sudo dnf install python3-pyyaml
```

## Exit Codes

- `0`: Job passed successfully
- `1`: Job failed, timed out, or was aborted
- `2`: Analysis error (cannot fetch artifacts, invalid URL, etc.)

## Examples

### Passing Job

```bash
./analyze.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1998111460479209472
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
./analyze.py https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-azure-ipi-kata/1998111457991987200
```

Output will include failure analysis with categorized failing tests, detected patterns, and root cause analysis.

### Presubmit/Rehearsal Job

```bash
./analyze.py https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/72608/rehearse-72608-periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate417-azure-ipi-coco/2001012534630420480
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

- Check that the Prow job URL is correct
- Verify the job has completed (or use --wait to wait for completion)
- Ensure you have network access to prow.ci.openshift.org

### "pyyaml not available"

Install the pyyaml library:
```bash
pip install pyyaml
```

### Job Still Running

Use the `--wait` option to wait for job completion:
```bash
./analyze.py --wait 600 <URL>  # Wait up to 10 minutes
```

## Development

### Running Tests

```bash
# Test with a known passing job
./analyze.py <passing-job-url>

# Test with a known failing job
./analyze.py <failing-job-url>

# Test JSON output
./analyze.py --json <job-url> | jq .
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
