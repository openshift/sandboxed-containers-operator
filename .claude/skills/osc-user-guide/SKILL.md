---
name: osc-user-guide
description: Provides comprehensive guidance on OpenShift Sandboxed Containers (OSC) by reading official Red Hat documentation. Use when questions involve OSC installation, KataConfig configuration, node setup, bare-metal or cloud deployments, PeerPods, confidential containers, TEE, Trustee, attestation, or troubleshooting.
---

# OSC User Guide Reader

When this skill is triggered, follow these steps to answer the user's question using the official OSC documentation:

## Instructions for Claude

1. **Determine the OSC version** to use:

   - If the user mentions a specific version (e.g., "with OSC 1.11"), use that version.
   - Otherwise, determine the latest version using the WebFetch tool:
   ```text
   WebFetch(url='https://docs.redhat.com/en/documentation/openshift_sandboxed_containers',
            prompt='What is the latest version of OpenShift Sandboxed Containers documentation? Return only the version number (e.g., 1.12)')
   ```

2. **Download PDFs** using the Bash tool with the version:
   ```bash
   bash .claude/skills/osc-user-guide/download-pdfs.sh <version>
   ```

   For example, if the version is 1.12:
   ```bash
   bash .claude/skills/osc-user-guide/download-pdfs.sh 1.12
   ```

   This script will:
   - Dynamically discover all available guides from the Red Hat docs site
   - Download PDFs into a per-version subdirectory (e.g., `.claude/skills/osc-user-guide/1.12/`)
   - Skip already-cached files
   - Works with any version — no hardcoded guide lists
   - No dependencies required (just curl or wget)

3. **Read the relevant PDF(s)** using Claude's built-in Read tool:

   The download script output will show the directory where PDFs are stored.
   Use the Read tool to access the documentation.

   **How to read PDFs:**
   - Use the Read tool with the `pages` parameter
   - Maximum 20 pages per Read call
   - Example: `Read(file_path='.claude/skills/osc-user-guide/1.12/<filename>.pdf', pages='1-20')`

   **Reading strategy:**
   - First, read the table of contents (typically pages 1-5) to understand the PDF structure
   - Then read only the specific pages relevant to the user's question
   - This is more efficient than reading the entire PDF
   - If needed, read additional PDFs using the same TOC-first approach

   **Guide selection — choose based on the user's question:**

   The download script discovers all available guides automatically. PDF filenames
   indicate their topic, e.g. `...-Deploying_confidential_containers_on_bare-metal_servers-en-US.pdf`.

   Guides fall into three categories:
   - **Deploying OpenShift Sandboxed Containers** — Operator install, KataConfig, kata runtime, peer pods
   - **Deploying Confidential Containers** — CoCo, TEE, kata-cc/kata-remote, initdata, attestation
   - **Deploying Red Hat Build of Trustee** — Trustee/KBS, attestation tokens, secrets

   Each category may have per-platform variants (bare-metal, Azure, AWS, IBM Z, etc.).
   Pick the guide matching the user's platform. If the user doesn't specify a platform, default to bare-metal.
   If unsure which guide, read the most relevant one first — it's usually enough.

4. **Answer the question comprehensively:**
   - Provide a direct answer to the user's question
   - Include step-by-step instructions when applicable
   - Show code examples and YAML configurations
   - List prerequisites and requirements
   - Cite which guide(s) and page numbers the information comes from
   - Mention related topics the user should know about

## How This Skill Works

This skill is **automatically triggered** by Claude when you ask questions about OpenShift Sandboxed Containers. You don't need to explicitly invoke it - just ask your question naturally.

When triggered, the skill:
1. Determines the OSC version (from user's question or latest available)
2. Dynamically discovers and downloads PDFs for that version (first run only, cached afterward)
3. Claude reads the table of contents first, then the relevant pages
4. Claude provides a comprehensive answer with examples and citations

Multiple versions can coexist — each version's PDFs are stored in their own subdirectory.

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

**Version-specific questions:**
- "Why is my KataConfig failing with OSC 1.11?"
- "What changed in OSC 1.12 for peer pods?"

**Trustee:**
- "How do I configure Red Hat build of Trustee?"
- "How do I create attestation tokens?"
- "What is the key broker service?"

**Troubleshooting:**
- "Why is my KataConfig failing?"
- "How do I verify Kata runtime installation?"

## Requirements

- `curl` or `wget` for downloading PDFs
- Internet connection (for initial download only)
- Read access to .claude/skills directory

## Setup for Team Members

**No manual setup required!**

The first time you run this skill:
1. Detects the OSC documentation version (from user's question or latest available)
2. Discovers and downloads all available PDF guides for that version from the Red Hat docs site

On subsequent runs, it uses the cached PDFs. Different versions can be cached simultaneously.
