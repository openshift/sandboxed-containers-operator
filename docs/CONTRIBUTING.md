# Contributing to OpenShift Sandboxed Containers Operator

Thank you for your interest in contributing to the OpenShift Sandboxed Containers Operator!

## Table of Contents

- [Getting Started](#getting-started)
- [Commit Message Format](#commit-message-format)
- [Pull Request Process](#pull-request-process)
- [Development Workflow](#development-workflow)
- [Code Review](#code-review)

## Getting Started

Before you begin:

1. **Read the Development Guide**: See [DEVELOPMENT.md](./DEVELOPMENT.md) for prerequisites, setup instructions, and build processes.
2. **Check existing JIRA issues**: We use JIRA for issue tracking. Check if your bug or feature has already been reported in the [KATA project](https://redhat.atlassian.net/jira/software/c/projects/KATA/summary).
3. **Join the community**: Our team can be found in #forum-sandboxed-containers (public) or #team-sandboxed-containers (private) channels at Red Hat's Slack.

## Commit Message Format

All commits must follow the convention adopted by this project. Before opening a PR, run the validation script to check your commits:

```bash
./hack/commit-msg-check.sh origin/devel HEAD
```

### Requirements

Each commit message must:

1. **Start with a subsystem prefix** followed by a colon (e.g., `ci:`, `docs:`, `feat:`, `fix:`)
2. **Have a subject line** that is **no longer than 80 characters**
3. **Include a body** that describes the changes in detail
4. **Body lines** should be **no longer than 150 characters** (lines starting with non-alphabetic characters like indented stack traces or code snippets are exempt)
5. **Include a Signed-off-by tag** (use `git commit -s` or `git commit --signoff`)

### Format

```
<subsystem>: <short summary (max 80 chars)>

<Detailed description of the change. Explain what and why, not how.
Body lines should be wrapped at 150 characters.>

Fixes: rhjira#KATA-XXXX

Signed-off-by: Your Name <your.email@example.com>
```

**Notes**:
- If your commit fixes or implements a JIRA issue, include `Fixes: rhjira#KATA-XXXX` in the commit body (replace XXXX with the actual issue number).
- Lines starting with non-alphabetic characters (spaces, numbers, special characters) are exempt from the 150-character limit. This includes indented stack traces, code snippets, and indented URLs.

### Common Subsystem Prefixes

- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `test:` - Test-related changes
- `ci:` - CI/CD pipeline changes
- `build:` - Build system or dependency changes
- `refactor:` - Code refactoring without functional changes
- `perf:` - Performance improvements
- `chore:` - Maintenance tasks (dependency updates, etc.)
- `api:` - API changes (KataConfig CRD, etc.)
- `controller:` - Controller logic changes
- `webhook:` - Webhook-related changes
- `peerpods:` - Peer-pods specific changes
- `monitor:` - Monitor component changes

### Examples

#### Good Commit Messages

```
feat: add support for custom kernel parameters in KataConfig

This change allows users to specify additional kernel parameters
through the KataConfig CR. The parameters are passed to the kata
runtime and applied when launching workloads.

Fixes: rhjira#KATA-1234

Signed-off-by: Jane Developer <jane@example.com>
```

```
fix: resolve memory leak in monitor container

The monitor container was not properly releasing resources when
pods were deleted, causing memory usage to grow over time.
Added proper cleanup in the deletion handler.

Signed-off-by: John Developer <john@example.com>
```

```
docs: update installation guide for OpenShift 4.15

Updated the documentation to reflect changes in the installation
process for OpenShift 4.15, including new prerequisites and
updated CLI commands.

Signed-off-by: Alice Writer <alice@example.com>
```

#### Bad Commit Messages

```
❌ Update code
   (Missing subsystem prefix, too vague, no body)

❌ feat: Added a new feature that allows users to configure custom runtime settings which is very useful
   (Subject line exceeds 80 characters)

❌ fix: bug fix
   (No descriptive body explaining what was fixed)

❌ Fixed the thing
   (Missing subsystem prefix, no body, vague)
```

## Pull Request Process

1. **Fork the repository** and create your branch from `devel` (our main development branch).

2. **Make your changes** following the commit message format above.

3. **Test your changes** thoroughly:
   - Run unit tests: `make test`
   - Build the operator: `make docker-build`
   - Test on an OpenShift cluster if possible

4. **Update documentation** if you're changing functionality or adding features.

5. **Fill out the PR template** with:
   - Description of the problem being solved
   - What you did
   - How to verify the changes
   - Changelog description

6. **Ensure CI passes**:
   - All tests must pass
   - Commit messages must pass validation
   - No merge conflicts

7. **Request reviews** from maintainers and address feedback promptly (see [code review](#code-review))

## Development Workflow

### Setting Up Your Development Environment

See [DEVELOPMENT.md](./DEVELOPMENT.md) for detailed instructions on:
- Installing prerequisites (Go 1.22.x, Operator SDK 1.39.1, etc.)
- Setting up container registry access
- Configuring environment variables
- Building and deploying the operator

### Making Changes

1. Create a feature branch:
   ```bash
   git checkout -b feat/your-feature-name
   ```

2. Make your changes and commit following the commit message format (use `git commit -s` to add Signed-off-by automatically).

3. Keep your commits focused and atomic - each commit should represent a single logical change and be buildable to maintain bisectability when debugging issues.

4. Validate your commits before pushing:
   ```bash
   ./hack/commit-msg-check.sh origin/devel HEAD
   ```

5. Push your changes and create a pull request.

### Running Tests

```bash
# Run unit tests
make test

# Build operator image
make docker-build
```

Run `make help` for further targets you may want to use to verify your changes.

## Code Review

Your changes must be reviewed by other developers of the project before getting merged.

In general, the proposed changes are expected to work correctly and integrate safely
without breaking existing behavior. Contributors should understand the implications of
their changes and be ready to answer questions or make adjustments if needed. It is
expected contributors to:

- **Be responsive**: Address review comments promptly.
- **Be open to feedback**: Reviewers are trying to help improve the code.
- **Keep discussions professional**: Focus on the code, not the person.

>**Important**: When addressing review feedback, amend your existing commits and
force-push to keep the history clean.

Reviewers will check for (amongst other things):
- Code correctness and functionality
- Test coverage
- Documentation updates
- Adherence to project conventions
- Security implications

Each change require to be merged:
- At least **two approvals** from approvers
- Passing CI checks (e.g. commit message format compliance) and tests (e.g. unit tests)

The list of reviewers and approvers is found at [OWNERS](./OWNERS) file. Your reviews
are welcomed even if you don't belong to that list, however, your approval won't sum up
to the two approvals requirement.

## Questions?

If you have questions about contributing:
- Check [DEVELOPMENT.md](./DEVELOPMENT.md) for development setup
- Review existing [pull requests](https://github.com/openshift/sandboxed-containers-operator/pulls) for examples
- Ask the team in #team-sandboxed-containers channel at Red Hat's Slack
- Create a JIRA issue in the KATA project for discussion

---
