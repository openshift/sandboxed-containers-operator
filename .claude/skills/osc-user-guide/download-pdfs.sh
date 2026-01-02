#!/bin/bash
# Download OSC User Guide PDFs by dynamically discovering them from the docs site.
# Works with any version (1.11, 1.12+, future versions) without hardcoded guide lists.
# PDFs are stored in per-version subdirectories (e.g. osc-user-guide/1.12/*.pdf).
set -e

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

version="$1"
if [ -z "${version}" ]; then
	echo -e "ERROR: Missing version argument\nUse $0 <version>"
	exit 1
fi
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+$ ]]; then
    echo "ERROR: Invalid version '${version}'. Expected format: X.Y (e.g., 1.12)"
    exit 1
fi

version_dir="${skill_dir}/${version}"
mkdir -p "${version_dir}"

docs_base="https://docs.redhat.com/en/documentation/openshift_sandboxed_containers/${version}"

fetch_url() {
    local url="$1"
    if command -v curl &> /dev/null; then
        curl --fail -sL "$url"
    elif command -v wget &> /dev/null; then
        wget -q -O- "$url"
    else
        echo "ERROR: Neither curl nor wget found." >&2
        exit 1
    fi
}

file_size_kb() {
    local file="$1"
    echo $(( $(stat -f%z "$file" 2>/dev/null || stat -c%s "$file") / 1024 ))
}

is_valid_pdf() {
    local file="$1"
    [ -f "$file" ] && [ "$(file_size_kb "$file")" -gt 1 ] && head -c 4 "$file" | grep -q '%PDF'
}

download_pdf() {
    local pdf_url="$1"
    local filename
    filename=$(basename "$pdf_url")
    local pdf_file="${version_dir}/${filename}"

    # Check cache
    if is_valid_pdf "$pdf_file"; then
        echo "  [cached] ${filename} ($(file_size_kb "$pdf_file")KB)"
        return 0
    fi
    rm -f "$pdf_file"

    echo "  [downloading] ${filename}..."

    if command -v curl &> /dev/null; then
        curl --fail -sL "$pdf_url" -o "$pdf_file" || { echo "  [FAILED] ${filename} - download error"; rm -f "$pdf_file"; return 1; }
    elif command -v wget &> /dev/null; then
        wget -q "$pdf_url" -O "$pdf_file" || { echo "  [FAILED] ${filename} - download error"; rm -f "$pdf_file"; return 1; }
    fi

    if is_valid_pdf "$pdf_file"; then
        echo "  [ok] ${filename} ($(file_size_kb "$pdf_file")KB)"
    else
        echo "  [FAILED] ${filename} - not a valid PDF, removing"
        rm -f "$pdf_file"
        return 1
    fi
}

echo "OSC ${version} User Guide PDFs"
echo "========================"
echo

# Step 1: Discover guide slugs from the version landing page
echo "Discovering guides for version ${version}..."
landing_page=$(fetch_url "${docs_base}") || {
    echo "ERROR: Could not fetch docs landing page for version ${version}."
    echo "Check that the version exists at: ${docs_base}"
    exit 1
}

slugs=$(echo "$landing_page" | \
    grep -oE "/en/documentation/openshift_sandboxed_containers/${version}/html/[^\"]+" | \
    sed 's|.*/html/||' | sort -u)

if [ -z "$slugs" ]; then
    echo "ERROR: No guides found for version ${version}."
    exit 1
fi

guide_count=$(echo "$slugs" | wc -l | tr -d ' ')
echo "Found ${guide_count} guides."
echo

# Step 2: For each slug, discover the PDF URL from the guide's HTML page and download it
for slug in $slugs; do
    slug_page=$(fetch_url "${docs_base}/html/${slug}" 2>/dev/null) || {
        echo "  [SKIP] ${slug} - could not fetch guide page"
        continue
    }
    pdf_path=$(echo "$slug_page" | \
        grep -oE "/en/documentation/openshift_sandboxed_containers/${version}/pdf/[^\"]+\.pdf" | \
        head -1)

    if [ -z "$pdf_path" ]; then
        echo "  [SKIP] ${slug} - could not find PDF link"
        continue
    fi

    download_pdf "https://docs.redhat.com${pdf_path}" || true
done

echo
echo "Download complete. PDFs are in: ${version_dir}"
