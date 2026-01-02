---
name: osc-user-guide
description: Provides comprehensive guidance on OpenShift Sandboxed Containers (OSC) by reading official Red Hat documentation. Use when questions involve OSC installation, KataConfig configuration, node setup, bare-metal or cloud deployments, PeerPods, confidential containers, TEE, Trustee, attestation, or troubleshooting.
---

# OSC User Guide Reader

When this skill is triggered, follow these steps to answer the user's question using the official OSC documentation:

## Instructions for Claude

1. **Run the documentation reader script** using the Bash tool:
   ```bash
   python3 .claude/skills/osc-user-guide/skill.py "<user's question>"
   ```

   This will:
   - Auto-setup Python environment (first run)
   - Download all three OSC 1.11 PDF guides (first run, ~2 MB)
   - Display paths to the available documentation
   - Show the user's question

2. **Search the PDFs efficiently** using the provided search helper:

   **IMPORTANT**:
   - Use the `search_pdfs.py` helper instead of writing custom search code
   - Always use the virtual environment Python: `.claude/skills/osc-user-guide/venv/bin/python3`

   **Note on Permissions**:
   - The first time you run the search tool, Claude will ask for permission
   - Select option 2: "Yes, and don't ask again for similar commands in this project"
   - This only needs to be done once per user/machine

   **Available search methods:**

   a) **Keyword search** - Find pages containing specific keywords:
      ```bash
      .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
        --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
               "Confidential=.claude/skills/osc-user-guide/osc-confidential-1.11.pdf" \
               "Trustee=.claude/skills/osc-user-guide/osc-trustee-1.11.pdf" \
        -k kataConfigPoolSelector checkNodeEligibility \
        -n 10
      ```

   b) **Topic search** - Find pages about a specific topic:
      ```bash
      .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
        --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
        -t "subset of nodes" "node selector" \
        -r installation deployment \
        -n 10
      ```

   c) **Section search** - Find specific section types:
      ```bash
      .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
        --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
        -s prerequisite requirements procedure \
        -n 10
      ```

   d) **Get specific page** - Retrieve a known page number:
      ```bash
      .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
        --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
        --page Deployment 15
      ```

   e) **Get page range** - Retrieve multiple consecutive pages:
      ```bash
      .claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py \
        --pdfs "Deployment=.claude/skills/osc-user-guide/osc-deployment-1.11.pdf" \
        --range Deployment 10 15
      ```

   **Search strategy:**
   - Start with keyword search using terms from the user's question
   - Use `-n 10` to get more results initially (default is 5)
   - If too many results, add more specific keywords or use `--all` to require all keywords
   - Use `--pdf <name>` to search only specific guide if question is clearly about one topic
   - After finding relevant pages, use `--page` or `--range` to get full content

3. **Analyze search results and answer** - Based on the search results:
   - Read the relevant page content returned by the search tool
   - If search returns too many or too few results, refine your search
   - Combine information from multiple pages if needed
   - Provide a comprehensive answer with:
     - Direct answer to the question
     - Step-by-step instructions (if applicable)
     - Code examples and YAML configurations
     - Prerequisites and requirements
     - Which guide(s) and page numbers the information comes from
     - Related topics the user should know about

## Available Tools

This skill includes several helper tools for efficient PDF searching:

### 1. `skill.py` - Main Skill Entry Point
Primary script that initializes the skill environment and documentation.
- Auto-creates Python virtual environment
- Downloads PDFs on first run
- Displays available documentation

### 2. `search_pdfs.py` - PDF Search Helper
**Use this for all PDF searches - do not write custom search code.**

Provides multiple search capabilities:
- **Keyword search**: Find pages containing specific terms
- **Topic search**: Find pages about a topic with optional required keywords
- **Section search**: Find specific section types (prerequisites, procedures, etc.)
- **Page retrieval**: Get specific pages or page ranges
- **Relevance scoring**: Results ranked by relevance

**IMPORTANT**: Always invoke with the virtual environment Python:
```bash
.claude/skills/osc-user-guide/venv/bin/python3 .claude/skills/osc-user-guide/search_pdfs.py --help
```

### 3. `pdf_utils.py` - PDF Utility Library
Low-level PDF search functions used by `search_pdfs.py`.
- `PDFSearcher` class for programmatic searching
- `PDFSearchResult` class for search results
- Helper functions for text extraction and scoring

**For Claude:** Always use `search_pdfs.py` CLI tool, not the utility library directly.

## About the Documentation

This skill provides access to all three OpenShift Sandboxed Containers 1.11 User Guides:

### 1. Deploying Red Hat OpenShift Sandboxed Containers
Topics covered:
- Operator installation and prerequisites
- KataConfig custom resource configuration
- Node Feature Discovery setup
- Bare metal and cloud deployments
- PeerPods configuration
- Troubleshooting and verification
- Runtime configuration options

### 2. Deploying Confidential Containers
Topics covered:
- Confidential containers deployment
- Trusted execution environment (TEE) setup
- Attestation and verification
- Encryption and secure enclaves
- Hardware requirements for confidential computing

### 3. Deploying Red Hat Build of Trustee
Topics covered:
- Trustee service deployment
- Attestation token management
- Secrets management and key broker service
- Container image verification
- Zero-trust architecture implementation

## How This Skill Works

This skill is **automatically triggered** by Claude when you ask questions about OpenShift Sandboxed Containers. You don't need to explicitly invoke it - just ask your question naturally.

When triggered, the skill:
1. Runs `python3 .claude/skills/osc-user-guide/skill.py "<your question>"`
2. Downloads documentation PDFs if needed (first run only)
3. Presents the PDFs for Claude to read and answer your question

## Example Questions That Trigger This Skill

Ask questions naturally - the skill will activate automatically:

**General deployment:**
- "How do I install the OSC operator?"
- "Show me KataConfig configuration options"
- "How do I deploy OSC on a subset of worker nodes?"
- "What are the node eligibility requirements?"

**Confidential containers:**
- "How do I deploy confidential containers?"
- "What hardware is required for TEE?"
- "How does attestation work in confidential containers?"

**Trustee:**
- "How do I configure Red Hat build of Trustee?"
- "How do I create attestation tokens?"
- "What is the key broker service?"

**Troubleshooting:**
- "Why is my KataConfig failing?"
- "How do I verify Kata runtime installation?"

## Documentation Sources

This skill fetches and reads from all three OSC 1.11 guides:

1. **Deploying Red Hat OpenShift Sandboxed Containers**
   - https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_red_hat_openshift_sandboxed_containers/

2. **Deploying Confidential Containers**
   - https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_confidential_containers/

3. **Deploying Red Hat Build of Trustee**
   - https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_red_hat_build_of_trustee/

## Requirements

- Python 3.6+ with venv module
- Internet connection (for initial download and dependency installation)
- Read access to .claude/skills directory

## Setup for Team Members

**No manual setup required!**

The first time you run this skill:
1. Automatically creates a Python virtual environment
2. Installs required dependencies (PyPDF2)
3. Downloads all three OSC User Guide PDFs (~2 MB total)
   - Deployment Guide
   - Confidential Containers Guide
   - Trustee Guide

On subsequent runs, it uses the cached environment and documentation.

## Team Collaboration

This skill is designed to work seamlessly across your team:
- Virtual environment is created locally (not committed to git)
- All PDFs are downloaded once per machine (not committed to git)
- `requirements.txt` ensures consistent dependencies
- No system-wide package installations required
- The skill automatically handles missing documentation gracefully
