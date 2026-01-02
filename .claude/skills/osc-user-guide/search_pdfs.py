#!/usr/bin/env python3
"""
Standalone PDF search tool.
Can be used to search any PDF documents without invoking Claude.
"""
import sys
import argparse
from pathlib import Path

# Add current directory to path for imports
SKILL_DIR = Path(__file__).parent
sys.path.insert(0, str(SKILL_DIR))

from pdf_utils import PDFSearcher, format_search_results


def parse_pdf_args(pdf_args):
    """
    Parse PDF arguments in the format: name=path

    Args:
        pdf_args: List of strings in format "name=path"

    Returns:
        Dictionary mapping PDF names to paths
    """
    pdf_paths = {}
    for arg in pdf_args:
        if '=' not in arg:
            print(f"Error: PDF argument must be in format 'name=path', got: {arg}")
            sys.exit(1)

        name, path_str = arg.split('=', 1)
        path = Path(path_str)

        if not path.exists():
            print(f"Warning: PDF not found: {path}")

        pdf_paths[name] = path

    return pdf_paths


def main():
    parser = argparse.ArgumentParser(
        description='Search PDF documents',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
PDF Specification:
  Specify PDFs using: name=path
  Example: "Deployment Guide=/path/to/deployment.pdf"

Search Examples:
  # Search for keywords in specific PDFs
  %(prog)s --pdfs "Guide=/path/to/guide.pdf" -k hardware processor intel

  # Search for sections across multiple PDFs
  %(prog)s --pdfs "Doc1=/path/1.pdf" "Doc2=/path/2.pdf" -s prerequisites requirements

  # Search for a topic with required keywords
  %(prog)s --pdfs "Guide=/path/guide.pdf" -t intel tdx amd -r tee hardware

  # Get a specific page
  %(prog)s --pdfs "Guide=/path/guide.pdf" --page "Guide" 14

  # Get a range of pages
  %(prog)s --pdfs "Guide=/path/guide.pdf" --range "Guide" 8 12
        """
    )

    parser.add_argument(
        '--pdfs',
        nargs='+',
        required=True,
        metavar='NAME=PATH',
        help='PDF files to search (format: name=path)'
    )

    parser.add_argument(
        '-k', '--keywords',
        nargs='+',
        help='Search for keywords (any match by default)'
    )

    parser.add_argument(
        '--all',
        action='store_true',
        help='Require all keywords to match (use with -k)'
    )

    parser.add_argument(
        '-s', '--sections',
        nargs='+',
        help='Search for section types (e.g., prerequisite, requirement)'
    )

    parser.add_argument(
        '-t', '--topic',
        nargs='+',
        help='Search for topic keywords'
    )

    parser.add_argument(
        '-r', '--required',
        nargs='+',
        help='Required keywords (use with -t)'
    )

    parser.add_argument(
        '--page',
        nargs=2,
        metavar=('PDF_NAME', 'PAGE_NUM'),
        help='Get a specific page from a PDF'
    )

    parser.add_argument(
        '--range',
        nargs=3,
        metavar=('PDF_NAME', 'START', 'END'),
        help='Get a range of pages from a PDF'
    )

    parser.add_argument(
        '--pdf',
        help='Search only specific PDF by name'
    )

    parser.add_argument(
        '-n', '--max-results',
        type=int,
        default=5,
        help='Maximum number of results (default: 5)'
    )

    parser.add_argument(
        '--list-pdfs',
        action='store_true',
        help='List provided PDFs and exit'
    )

    args = parser.parse_args()

    # Parse PDF arguments
    pdf_paths = parse_pdf_args(args.pdfs)

    # List PDFs if requested
    if args.list_pdfs:
        print("Provided PDFs:")
        for name, path in pdf_paths.items():
            exists = "✓" if path.exists() else "✗"
            size = f"{path.stat().st_size / 1024 / 1024:.2f} MB" if path.exists() else "N/A"
            print(f"  {exists} {name}: {path} ({size})")
        return

    # Verify at least one search method is specified
    if not any([args.keywords, args.sections, args.topic, args.page, args.range]):
        parser.error("Specify at least one search method: -k, -s, -t, --page, or --range")

    # Check PDF availability
    available_pdfs = {name: path for name, path in pdf_paths.items() if path.exists()}
    missing = set(pdf_paths.keys()) - set(available_pdfs.keys())

    if missing:
        print(f"Warning: Missing PDFs: {', '.join(missing)}\n")

    if not available_pdfs:
        print("Error: No PDFs available.")
        sys.exit(1)

    # Determine which PDFs to search
    if args.pdf:
        if args.pdf not in available_pdfs:
            print(f"Error: PDF '{args.pdf}' not found in provided PDFs")
            sys.exit(1)
        search_pdfs = [args.pdf]
    else:
        search_pdfs = None  # Search all

    # Create searcher
    searcher = PDFSearcher(available_pdfs)

    # Perform search
    results = []

    if args.page:
        pdf_name, page_num = args.page
        page_num = int(page_num)
        text = searcher.get_page(pdf_name, page_num)
        if text:
            print(f"{'=' * 80}")
            print(f"{pdf_name} - Page {page_num}")
            print(f"{'=' * 80}")
            print(text)
        else:
            print(f"Error: Could not retrieve page {page_num} from {pdf_name}")
        return

    if args.range:
        pdf_name, start, end = args.range
        start, end = int(start), int(end)
        pages = searcher.get_pages_range(pdf_name, start, end)
        if pages:
            for page_num, text in pages:
                print(f"{'=' * 80}")
                print(f"{pdf_name} - Page {page_num}")
                print(f"{'=' * 80}")
                print(text)
                print()
        else:
            print(f"Error: Could not retrieve pages {start}-{end} from {pdf_name}")
        return

    if args.keywords:
        results = searcher.search_by_keywords(
            keywords=args.keywords,
            pdf_names=search_pdfs,
            match_all=args.all,
            max_results=args.max_results
        )
        print(f"\nKeyword search: {', '.join(args.keywords)}")
        print(f"Match mode: {'all keywords' if args.all else 'any keyword'}\n")

    elif args.sections:
        results = searcher.search_sections(
            section_types=args.sections,
            pdf_names=search_pdfs,
            max_results=args.max_results
        )
        print(f"\nSection search: {', '.join(args.sections)}\n")

    elif args.topic:
        results = searcher.search_topic(
            topic_keywords=args.topic,
            required_keywords=args.required,
            pdf_names=search_pdfs,
            max_results=args.max_results
        )
        print(f"\nTopic search: {', '.join(args.topic)}")
        if args.required:
            print(f"Required keywords: {', '.join(args.required)}")
        print()

    # Display results
    if results:
        print(format_search_results(results))
    else:
        print("No results found.")


if __name__ == '__main__':
    main()
