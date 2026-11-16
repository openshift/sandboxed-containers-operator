---
name: prowjob-analyzer
description: Analyze OpenShift Prow job results and OSC test failures using the sandboxed-containers-operator prowjob-analyzer tools.
compatibility: Requires Python 3 and an OSC source checkout; remote jobs require network access.
---

# Prow Job Analyzer

Use this skill to analyze Prow jobs, especially OpenShift Sandboxed Containers
(OSC) jobs. The canonical source is
https://github.com/openshift/sandboxed-containers-operator, branch `devel`.

Use the OSC checkout containing this skill, even if the current working
directory is another project. Resolve any symlinks in the skill directory;
the checkout root is three directories above its `SKILL.md`. If the client
does not expose the skill location, find the checkout from the current project
or configured references. Verify that `<OSC_ROOT>/scripts/prowjob-analyzer/`
exists and read its `README.md` for current usage. If the checkout or tools
are unavailable, ask for their location rather than assuming a user's home
directory or silently using a different version.

## Workflow

1. Run `python3 <OSC_ROOT>/scripts/prowjob-analyzer/dig.py --no-wait <PROW_JOB_URL>`.
   For a local artifact directory or tarball, use `--artifacts <path>` instead.
   If a Deck history URL is rejected, retain its bucket and job path and use
   the equivalent `https://prow.ci.openshift.org/view/gs/...` URL.
2. Review the job status, environment, result categories, failed steps, test
   summaries, and artifact links. Distinguish `skipped`, incomplete results,
   `TEST_BUG`, and actual test failures. The post-phase OSC, Kata upstream,
   and CAA suites can fail independently; `best_effort` does not make a
   failing job pass.
3. For tests that actually ran and failed, run
   `python3 <OSC_ROOT>/scripts/prowjob-analyzer/dig_failed_tests_report.py <PROW_JOB_URL>`
   and inspect the relevant JUnit and step logs. For infrastructure, sanity,
   compilation, or suite startup failures without real failed tests, inspect
   the failed step's own artifacts instead. For local `--artifacts` input,
   inspect local JUnit and logs directly; the detail script requires a URL.
4. When most tests fail, investigate the earliest actual test and setup logs
   for a shared cause, but verify execution order and do not assume later
   failures are cascading. An initial RPM dependency failure, for example,
   may be followed by repeated hotfix/unlocked-state errors.
5. Report the observed evidence, affected suites and tests, likely causes with
    uncertainty, useful artifact links, and actionable next steps. Do not treat
    the analyzer's generic pattern classification as a confirmed root cause.

## Output format

- Present jobs as a list, not a table. Each item begins with the job's start
  time, followed by the job name or link, then its result.
- Use the spreadsheet `start_time` format exactly as reported by the analyzer
  (e.g. `2026-11-12T00:15:41Z`); use `unknown` if unavailable.
- Use `SUCCESS` for a passed job. For failures, use `FAILURE` followed by a
  concise finding; preserve other states such as pending or aborted rather
  than treating them as successful. Keep skipped suites distinct from passes.
