# OSC User Guide Skill

A Claude Code skill that provides comprehensive access to all three OpenShift Sandboxed Containers 1.11 documentation guides.

## Overview

This skill automatically triggers when you ask questions about OpenShift Sandboxed Containers. It provides efficient PDF search capabilities across three official Red Hat documentation guides.

## Documentation Covered

1. **Deploying Red Hat OpenShift Sandboxed Containers** (0.67 MB)
   - Operator installation and prerequisites
   - KataConfig custom resource configuration
   - Node Feature Discovery setup
   - Bare metal and cloud deployments
   - PeerPods configuration

2. **Deploying Confidential Containers** (0.49 MB)
   - Confidential containers deployment
   - Trusted Execution Environment (TEE) setup
   - Attestation and verification
   - Encryption and secure enclaves

3. **Deploying Red Hat Build of Trustee** (0.48 MB)
   - Trustee service deployment
   - Attestation token management
   - Secrets management
   - Key broker service

## Files

- `SKILL.md` - Skill definition with instructions for Claude
- `skill.py` - Main entry point (handles environment setup and PDF downloads)
- `search_pdfs.py` - PDF search helper tool (use this for all searches)
- `pdf_utils.py` - PDF utility library (low-level functions)
- `requirements.txt` - Python dependencies
- `.gitignore` - Excludes venv and PDFs from git

## How It Works

### For Users

Just ask your question naturally:
```
How do I install OSC on a subset of worker nodes?
```

Claude will automatically:
1. Detect this is an OSC question
2. Trigger the osc-user-guide skill
3. Search the documentation
4. Provide a comprehensive answer

### For Claude

When the skill is triggered, Claude:
1. Runs `skill.py` to ensure environment is set up
2. Uses `search_pdfs.py` to search for relevant content
3. Reads the search results and formulates an answer

## Tools

### search_pdfs.py - PDF Search Helper

**ALWAYS use the virtual environment Python:**
```bash
.claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py
```

**Note on Permissions:**
The first time you use the search tool, Claude will ask for permission. Select option 2: "Yes, and don't ask again for similar commands in this project". This only needs to be done once per user/machine.

**Search methods:**

1. **Keyword search** - Find pages with specific keywords:
   ```bash
   .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
     --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
     -k kataConfig installation \
     -n 10
   ```

2. **Topic search** - Find pages about a topic:
   ```bash
   .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
     --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
     -t "peer pods" deployment \
     -r cloud azure \
     -n 10
   ```

3. **Section search** - Find specific section types:
   ```bash
   .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
     --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
     -s prerequisite procedure \
     -n 10
   ```

4. **Get specific page**:
   ```bash
   .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
     --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
     --page Deployment 26
   ```

5. **Get page range**:
   ```bash
   .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
     --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
     --range Deployment 20 30
   ```

## Setup for Team Members

**No manual setup required!**

First-time run automatically:
1. Creates Python virtual environment
2. Installs dependencies (PyPDF2)
3. Downloads all three PDF guides (~2 MB total)

Subsequent runs use cached environment and documentation.

## Example Search

```bash
# Search for information about installing on specific nodes
.claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
  --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
  -k kataConfigPoolSelector checkNodeEligibility \
  -n 5
```

This returns pages containing information about:
- Using kataConfigPoolSelector to target specific nodes
- Setting checkNodeEligibility for node validation
- Labeling nodes with feature.node.kubernetes.io/runtime.kata=true

## Tips

- Use `-n 10` for more results (default is 5)
- Use `--all` with `-k` to require all keywords (AND logic)
- Use `--pdf <name>` to search only one guide
- Combine searches: start broad, then narrow with page retrieval
- Always cite page numbers in answers

## Updating Documentation

To update to a newer version:
1. Update the URLs in `skill.py`
2. Delete cached PDFs: `rm .claude/skills/osc-user-guide/*.pdf`
3. Run skill again to download new versions
