#!/usr/bin/env python3
"""
OSC User Guide documentation reader skill.
Searches and extracts information from all OSC User Guide PDFs.
"""
import sys
import os
import subprocess
import urllib.request
import argparse
from pathlib import Path

# Path to store the documentation
SKILL_DIR = Path(__file__).parent
VENV_DIR = SKILL_DIR / 'venv'
REQUIREMENTS = SKILL_DIR / 'requirements.txt'

# All OSC 1.11 documentation guides
DOCUMENTATION = {
    'deployment': {
        'name': 'Deploying Red Hat OpenShift Sandboxed Containers',
        'path': SKILL_DIR / 'osc-deployment-1.11.pdf',
        'url': 'https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_red_hat_openshift_sandboxed_containers/OpenShift_sandboxed_containers-1.11-Deploying_Red_Hat_OpenShift_sandboxed_containers-en-US.pdf',
        'topics': 'bare-metal, cloud deployments, Kata installation, KataConfig, node configuration'
    },
    'confidential': {
        'name': 'Deploying Confidential Containers',
        'path': SKILL_DIR / 'osc-confidential-1.11.pdf',
        'url': 'https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_confidential_containers/OpenShift_sandboxed_containers-1.11-Deploying_confidential_containers-en-US.pdf',
        'topics': 'confidential containers, trusted execution environment, attestation, encryption'
    },
    'trustee': {
        'name': 'Deploying Red Hat Build of Trustee',
        'path': SKILL_DIR / 'osc-trustee-1.11.pdf',
        'url': 'https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/1.11/pdf/deploying_red_hat_build_of_trustee/OpenShift_sandboxed_containers-1.11-Deploying_Red_Hat_build_of_Trustee-en-US.pdf',
        'topics': 'Trustee, attestation tokens, secrets management, key broker service'
    }
}

def setup_environment():
    """Setup virtual environment and install dependencies if needed."""
    if not VENV_DIR.exists():
        print("First-time setup: Creating Python virtual environment...")
        subprocess.check_call([sys.executable, '-m', 'venv', str(VENV_DIR)])
        print("Virtual environment created.")
        print()

    # Check if PyPDF2 is installed
    pip_path = VENV_DIR / 'bin' / 'pip'
    try:
        result = subprocess.run(
            [str(pip_path), 'show', 'PyPDF2'],
            capture_output=True,
            text=True
        )
        if result.returncode != 0:
            # PyPDF2 not installed, install requirements
            print("Installing dependencies...")
            subprocess.check_call([str(pip_path), 'install', '-q', '-r', str(REQUIREMENTS)])
            print("Dependencies installed successfully.")
            print()
    except Exception as e:
        print(f"Warning: Could not verify dependencies: {e}")
        print()

def download_docs_if_needed():
    """Download all user guides if not already present."""
    downloaded_docs = []
    missing_docs = []

    for doc_key, doc_info in DOCUMENTATION.items():
        doc_path = doc_info['path']
        doc_url = doc_info['url']
        doc_name = doc_info['name']

        if not doc_path.exists():
            print(f"Downloading: {doc_name}...")
            print(f"  Source: {doc_url}")
            print(f"  Destination: {doc_path.name}")
            try:
                urllib.request.urlretrieve(doc_url, str(doc_path))
                size_mb = doc_path.stat().st_size / (1024 * 1024)
                print(f"  ✓ Download complete ({size_mb:.2f} MB)")
                print()
                downloaded_docs.append(doc_info)
            except Exception as e:
                print(f"  ✗ Error downloading: {e}")
                print()
                missing_docs.append((doc_name, doc_url))
        else:
            size_mb = doc_path.stat().st_size / (1024 * 1024)
            downloaded_docs.append(doc_info)

    # Summary
    if downloaded_docs:
        print(f"Available documentation guides: {len(downloaded_docs)}/3")
        for doc in downloaded_docs:
            size_mb = doc['path'].stat().st_size / (1024 * 1024)
            print(f"  ✓ {doc['name']} ({size_mb:.2f} MB)")
        print()

    return downloaded_docs, missing_docs

def main():
    if len(sys.argv) < 2:
        print("Usage: /osc-user-guide <question>")
        print()
        print("Examples:")
        print("  /osc-user-guide how to install the operator")
        print("  /osc-user-guide KataConfig configuration options")
        print("  /osc-user-guide how to deploy confidential containers")
        print("  /osc-user-guide Trustee attestation setup")
        sys.exit(1)

    query = ' '.join(sys.argv[1:])

    print("=" * 80)
    print("OSC USER GUIDE READER - Version 1.11")
    print("=" * 80)
    print()

    # Setup environment (creates venv and installs deps if needed)
    setup_environment()

    # Ensure all documentation is available
    available_docs, missing_docs = download_docs_if_needed()

    print("Question:", query)
    print()
    print("-" * 80)
    print()

    if available_docs:
        print("Claude, please search and read from these OpenShift Sandboxed Containers guides:")
        print()
        for doc in available_docs:
            print(f"  📄 {doc['name']}")
            print(f"     Path: {doc['path']}")
            print(f"     Topics: {doc['topics']}")
            print()

        print(f"Question to answer: {query}")
        print()
        print("Instructions:")
        print("- Search across ALL available guides to find the most relevant information")
        print("- Provide a complete answer with:")
        print("  • Direct answer to the question")
        print("  • Step-by-step instructions if applicable")
        print("  • Code examples or YAML configurations if relevant")
        print("  • Prerequisites or requirements")
        print("  • Specify which guide(s) the information comes from")
        print("  • Related sections the user should know about")

        if missing_docs:
            print()
            print("Note: Some documentation could not be downloaded:")
            for doc_name, doc_url in missing_docs:
                print(f"  - {doc_name}")
                print(f"    You can try fetching from: {doc_url}")
    else:
        print("Could not download any documentation guides.")
        print()
        print("Claude, please try to fetch information from these online sources:")
        for doc_key, doc_info in DOCUMENTATION.items():
            print(f"  - {doc_info['name']}: {doc_info['url']}")
        print()
        print(f"Question: {query}")

if __name__ == '__main__':
    main()
