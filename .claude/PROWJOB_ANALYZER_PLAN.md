# Prow Job Analyzer Implementation Plan

## Overview
Create a Claude Code slash command `/prowjob-analyze` that analyzes OSC Prow job results to determine if human intervention is needed, extract metadata, and provide detailed failure analysis.

## User Requirements Summary
- **Scope**: OSC-specific analyzer with Kata/PeerPods knowledge
- **Output**: Human-readable markdown by default (--json flag for machine format)
- **Behavior**: Wait and retry if job still running (artifacts not ready)
- **Integration**: Claude Code slash command in this repository

## Implementation Architecture

### Technology Stack
- **Primary Language**: Python 3 (better JSON/YAML handling than bash)
- **Command Interface**: Claude Code slash command (`.claude/commands/prowjob-analyze.md`)
- **Pattern**: Modular design similar to must-gather plugin

### File Structure
```
.claude/
└── commands/
    └── prowjob-analyze.md              # Slash command definition

scripts/
└── prowjob-analyzer/                   # Analyzer directory
    ├── analyze.py                      # Main orchestrator
    ├── lib/                            # Analysis modules
    │   ├── __init__.py
    │   ├── fetcher.py                 # Fetch artifacts from GCS
    │   ├── parser.py                  # Parse prowjob.json, test-results.yaml
    │   ├── metadata_extractor.py      # Extract job metadata
    │   ├── failure_analyzer.py        # Analyze failures
    │   └── report_generator.py        # Generate reports
    └── README.md                       # Documentation
```

## Core Components

### 1. URL Parser and Artifact Fetcher (`lib/fetcher.py`)
**Purpose**: Convert Prow UI URLs to GCS artifact URLs and fetch files

**Key Patterns** (validated from existing test scripts):
- Input: `https://prow.ci.openshift.org/view/gs/test-platform-results/logs/{JOB_NAME}/{BUILD_ID}`
- Artifacts: Same URL + `/artifacts/{VARIANT}/...`
- Variant extraction: Parse from job name (e.g., `aws-ipi-peerpods`)

**Functions**:
- `parse_prow_url(url)` - Extract job name and build ID
- `fetch_artifact(url, path)` - Download artifact with retry logic
- `wait_for_artifacts(url, timeout=300)` - Poll until artifacts ready

### 2. Metadata Extractor (`lib/metadata_extractor.py`)
**Purpose**: Extract all job metadata from prowjob.json and artifacts

**Metadata to Extract**:
- Provider (AWS, Azure, GCP) - from job name
- OCP version - from prowjob.json spec
- Build tag/image - from job environment variables
- Workload type (kata, peerpods, coco) - from job name
- Trigger source (Konflux, periodic, presubmit) - from prowjob.json
- Kata RPM version - from `sandboxed-containers-operator-get-kata-rpm` step artifacts
- Variant - extracted from job name for artifact paths

### 3. Pass/Fail Determiner (`lib/parser.py`)
**Purpose**: Determine job status and parse test results

**Status Determination**:
- Parse `prowjob.json` for overall status
- Parse `test-results.yaml` for test outcomes (failures: 0 = pass)
- Distinguish: success, failure, timeout, error, aborted

### 4. Failure Analyzer (`lib/failure_analyzer.py`)
**Purpose**: Identify WHERE and WHY failure occurred

**Analysis Logic**:
- **Test Step Failures**: Parse test-results.yaml, categorize by sig/area
- **Prow Step Failures**: Check prowjob.json for failed steps
- **Pattern Recognition**: Detect OOM, timeout, network, image pull errors
- **Human Intervention Check**: Apply heuristics to determine if automated retry would help

**Common Patterns to Detect**:
- Timeout (context deadline exceeded)
- OOM (OOMKilled, out of memory)
- Network (connection refused, i/o timeout)
- Infrastructure (cluster provisioning failures)
- Configuration (wrong parameters, version mismatches)

### 5. Report Generator (`lib/report_generator.py`)
**Purpose**: Generate human-readable markdown and optional JSON reports

**Human Report Structure**:
```markdown
# Prow Job Analysis

## Status
✅ Passed / ❌ Failed / ⏱️ Timeout

## Job Overview
- Job Name: ...
- Build ID: ...
- Duration: ...
- URL: [Link]

## Environment
- Provider: AWS
- OCP Version: 4.19
- Workload: PeerPods
- Kata RPM: 3.21.0-3.rhaos4.19.el9
- Build: quay.io/...@sha256:...
- Trigger: Konflux

## Test Results
- Total: 36
- Passed: 35
- Failed: 1
- Skipped: 0

## [If Failed] Failure Analysis
### Failure Location: Test Step / Prow Step / Infrastructure

### Failed Tests (grouped by category)
#### Deployment (3 tests)
- Test name 1
- Test name 2

### Root Cause
- Pattern: timeout
- Suggested Actions: ...

## Human Intervention Required
✅ Yes / ⏸️ Safe to Retry / ❌ No (infra issue)

## Artifacts
- [Test Results](link)
- [Logs](link)
- [Must-Gather](link)
```

**JSON Report**: Structured data for automation (with --json flag)

## Implementation Plan

### Phase 1: Basic Infrastructure (Files to Create)
1. **`.claude/commands/prowjob-analyze.md`** - Slash command definition
   - Command syntax and usage examples
   - Invoke Python script with URL parameter

2. **`.claude/scripts/prowjob-analyzer/analyze.py`** - Main script
   - CLI argument parsing (URL, --json, --verbose, --wait)
   - Coordinate all analysis modules
   - Error handling and user feedback

3. **`.claude/scripts/prowjob-analyzer/lib/fetcher.py`** - Artifact fetcher
   - URL parsing and validation
   - Artifact download with retry
   - Wait for in-progress jobs (poll with timeout)

### Phase 2: Analysis Logic
4. **`.claude/scripts/prowjob-analyzer/lib/parser.py`** - Data parser
   - Parse prowjob.json (job status, spec, env vars)
   - Parse test-results.yaml (test outcomes)
   - Utility functions for YAML/JSON

5. **`.claude/scripts/prowjob-analyzer/lib/metadata_extractor.py`** - Metadata extraction
   - Extract provider, OCP version, workload from job name/spec
   - Fetch Kata RPM version from step artifacts
   - Build variant path for artifacts

### Phase 3: Failure Analysis
6. **`.claude/scripts/prowjob-analyzer/lib/failure_analyzer.py`** - Failure analysis
   - Identify failure location (test vs prow step)
   - Categorize failing tests
   - Pattern recognition for common failures
   - Human intervention determination

### Phase 4: Reporting
7. **`.claude/scripts/prowjob-analyzer/lib/report_generator.py`** - Report generation
   - Generate markdown report
   - Generate JSON report (optional)
   - Format test failures with grouping
   - Add artifact links

8. **`.claude/scripts/prowjob-analyzer/README.md`** - Documentation
   - Usage instructions
   - Architecture overview
   - Development guide

## Key Technical Decisions

### URL Handling
- Use existing test scripts as reference (`tests/test-get-files.sh`)
- Follow redirects with curl/requests
- Variant extraction: Parse job name to get `{provider}-ipi-{workload}`

### Artifact Paths
Based on validated patterns:
```
{PROWJOB_URL}/prowjob.json                                           # Job metadata
{PROWJOB_URL}/finished.json                                          # Completion data
{PROWJOB_URL}/artifacts/{VARIANT}/openshift-extended-test/artifacts/test-results.yaml
{PROWJOB_URL}/artifacts/{VARIANT}/openshift-extended-test/artifacts/extended.log
{PROWJOB_URL}/artifacts/{VARIANT}/sandboxed-containers-operator-get-kata-rpm/artifacts/kata-rpm-version.txt
{PROWJOB_URL}/artifacts/{VARIANT}/sandboxed-containers-operator-gather-must-gather/artifacts/
```

### Error Handling
- **In-Progress Jobs**: Poll for up to 5 minutes, show progress
- **Missing Artifacts**: Graceful degradation, analyze what's available
- **Network Errors**: Retry with exponential backoff (3 attempts)
- **Invalid URLs**: Validate format, suggest correct pattern

### OSC-Specific Features
- Recognize OSC job patterns (job name prefixes)
- Extract Kata RPM version (OSC-specific step)
- Understand workload types (kata, peerpods, coco)
- Categorize tests by sig-kata patterns
- Check version mismatches (EXPECTED_OPERATOR_VERSION)

## Dependencies
- Python 3 (available)
- `pyyaml` - YAML parsing
- `requests` - HTTP client (or use urllib3 built-in)

## Testing Strategy
- Test with real Prow job URLs from user examples
- Handle both passing and failing jobs
- Verify all metadata extraction
- Validate report format
- Test edge cases (in-progress, missing artifacts, timeouts)

## Critical Files Created
1. `.claude/commands/prowjob-analyze.md` - Slash command definition
2. `scripts/prowjob-analyzer/analyze.py` - Main analyzer script
3. `scripts/prowjob-analyzer/lib/fetcher.py` - Artifact fetcher
4. `scripts/prowjob-analyzer/lib/parser.py` - Data parser
5. `scripts/prowjob-analyzer/lib/metadata_extractor.py` - Metadata extraction
6. `scripts/prowjob-analyzer/lib/failure_analyzer.py` - Failure analysis
7. `scripts/prowjob-analyzer/lib/report_generator.py` - Report generation
8. `scripts/prowjob-analyzer/README.md` - Documentation

## Success Criteria
- `/prowjob-analyze <URL>` command works in Claude Code
- Extracts all required metadata accurately
- Correctly determines pass/fail status
- Identifies failure locations and provides details
- Generates readable reports with artifact links
- Handles in-progress jobs gracefully
- OSC-specific analysis (Kata RPM, workload types)
