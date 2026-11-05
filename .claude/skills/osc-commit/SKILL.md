---
name: osc-commit
description: >-
  Validates and creates commits in the sandboxed-containers-operator repo.
  Enforces commit message format, sign-off, and pre-commit checks from
  docs/CONTRIBUTING.md. Trigger when the user asks to commit changes.
---

# OSC Commit Skill

When this skill is triggered, follow every step below before creating the commit.
Do not skip steps. If any step fails, stop and report the failure.

## 1. Pre-flight checks

Determine which Go modules were affected by the staged changes, then run the
appropriate build and lint commands.

**Operator root module** (changes outside `test/e2e/`):

```bash
make fmt vet
go build ./...
```

**E2E test module** (changes under `test/e2e/`):

```bash
cd test/e2e
go build ./...
go vet ./...
```

If both modules are affected, validate both. If gofmt or `make fmt` modifies
files, re-stage them before proceeding.

## 2. Stage files

- Stage only the files relevant to the change. Never `git add -A`.
- After editing any already-staged file, re-stage it.
- Verify with `git status` — no unexpected files staged.
- Never stage files that may contain secrets (`.env`, credentials, tokens).

## 3. Compose the commit message

Follow the format from `docs/CONTRIBUTING.md`:

```
<subsystem>: <short summary>

<Body: what changed and why. Wrap at 150 chars.>

Signed-off-by: (added by git commit -s)
```

### Rules

- **Subsystem prefix required** — one of:
  `feat:` `fix:` `docs:` `test:` `ci:` `build:` `refactor:` `perf:` `chore:`
  `api:` `controller:` `webhook:` `peerpods:` `monitor:`
- **Subject line max 80 characters** (including prefix)
- **Body required** — explain what and why, not how
- **Body lines max 150 characters** (lines starting with non-alpha chars are exempt)
- If fixing a JIRA issue, add `Fixes: rhjira#KATA-XXXX` in the body
- **Never add Co-Authored-By Claude tags**
- **Never modify git config** (user.name, user.email)

## 4. Commit

Always use `git commit -s` to add the Signed-off-by automatically.
Let `git commit -s` use git config for sign-off — never fabricate identity.

Use a HEREDOC to pass the message:

```bash
git commit -s -m "$(cat <<'EOF'
<subsystem>: <summary>

<body>
EOF
)"
```

## 5. Post-commit validation

Run the commit message checker:

```bash
./hack/commit-msg-check.sh origin/devel HEAD
```

If it fails, the commit message does not meet project standards.
Do NOT amend — create a new commit with the corrected message after
resetting the failed one (`git reset HEAD~1` then re-stage and re-commit).

## 6. Report

Show the user:
- `git log --oneline -1` — the commit
- `git diff --stat HEAD~1` — what changed
- Result of `commit-msg-check.sh`
