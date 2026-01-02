#!/usr/bin/env python3
"""
PDF search and extraction utilities for OSC User Guide documentation.
Provides reusable functions to search and extract content from PDF files.
"""
import re
from pathlib import Path
from typing import List, Dict, Tuple, Optional

try:
    import PyPDF2
except ImportError:
    PyPDF2 = None


class PDFSearchResult:
    """Represents a search result from a PDF."""

    def __init__(self, pdf_name: str, page_num: int, text: str, relevance_score: float = 0.0):
        self.pdf_name = pdf_name
        self.page_num = page_num  # 1-based page number
        self.text = text
        self.relevance_score = relevance_score

    def __repr__(self):
        return f"PDFSearchResult(pdf={self.pdf_name}, page={self.page_num}, score={self.relevance_score:.2f})"


class PDFSearcher:
    """Utility class for searching PDF documents."""

    def __init__(self, pdf_paths: Dict[str, Path]):
        """
        Initialize the PDF searcher.

        Args:
            pdf_paths: Dictionary mapping PDF names to their file paths
        """
        if PyPDF2 is None:
            raise ImportError("PyPDF2 is required but not installed")

        self.pdf_paths = pdf_paths
        self._loaded_pdfs = {}

    def _load_pdf(self, pdf_name: str) -> Optional[PyPDF2.PdfReader]:
        """Load a PDF file if not already loaded."""
        if pdf_name in self._loaded_pdfs:
            return self._loaded_pdfs[pdf_name]

        pdf_path = self.pdf_paths.get(pdf_name)
        if not pdf_path or not pdf_path.exists():
            return None

        try:
            with open(pdf_path, 'rb') as f:
                pdf = PyPDF2.PdfReader(f)
                self._loaded_pdfs[pdf_name] = pdf
                return pdf
        except Exception as e:
            print(f"Error loading PDF {pdf_name}: {e}")
            return None

    def search_by_keywords(
        self,
        keywords: List[str],
        pdf_names: Optional[List[str]] = None,
        match_all: bool = False,
        max_results: int = 10,
        context_chars: int = 2000
    ) -> List[PDFSearchResult]:
        """
        Search for keywords across PDF documents.

        Args:
            keywords: List of keywords to search for
            pdf_names: Optional list of PDF names to search (searches all if None)
            match_all: If True, all keywords must be present; if False, any keyword matches
            max_results: Maximum number of results to return
            context_chars: Number of characters to include in result text

        Returns:
            List of PDFSearchResult objects, sorted by relevance
        """
        results = []
        search_pdfs = pdf_names or list(self.pdf_paths.keys())

        for pdf_name in search_pdfs:
            pdf_path = self.pdf_paths.get(pdf_name)
            if not pdf_path or not pdf_path.exists():
                continue

            try:
                with open(pdf_path, 'rb') as f:
                    pdf = PyPDF2.PdfReader(f)

                    for page_num, page in enumerate(pdf.pages, start=1):
                        text = page.extract_text()
                        lower_text = text.lower()

                        # Calculate relevance score
                        keyword_matches = sum(1 for kw in keywords if kw.lower() in lower_text)

                        if match_all:
                            # All keywords must be present
                            if keyword_matches < len(keywords):
                                continue
                        else:
                            # At least one keyword must be present
                            if keyword_matches == 0:
                                continue

                        # Calculate relevance score (0-1)
                        relevance = keyword_matches / len(keywords)

                        # Limit text length
                        display_text = text[:context_chars] if len(text) > context_chars else text

                        results.append(PDFSearchResult(
                            pdf_name=pdf_name,
                            page_num=page_num,
                            text=display_text,
                            relevance_score=relevance
                        ))
            except Exception as e:
                print(f"Error searching {pdf_name}: {e}")
                continue

        # Sort by relevance score (highest first)
        results.sort(key=lambda x: x.relevance_score, reverse=True)

        return results[:max_results]

    def search_sections(
        self,
        section_types: List[str],
        pdf_names: Optional[List[str]] = None,
        max_results: int = 10,
        context_chars: int = 2500
    ) -> List[PDFSearchResult]:
        """
        Search for specific section types (e.g., prerequisites, requirements).

        Args:
            section_types: Types of sections to find (e.g., ['prerequisite', 'requirement'])
            pdf_names: Optional list of PDF names to search
            max_results: Maximum number of results
            context_chars: Characters to include in results

        Returns:
            List of PDFSearchResult objects
        """
        results = []
        search_pdfs = pdf_names or list(self.pdf_paths.keys())

        for pdf_name in search_pdfs:
            pdf_path = self.pdf_paths.get(pdf_name)
            if not pdf_path or not pdf_path.exists():
                continue

            try:
                with open(pdf_path, 'rb') as f:
                    pdf = PyPDF2.PdfReader(f)

                    for page_num, page in enumerate(pdf.pages, start=1):
                        text = page.extract_text()
                        lower_text = text.lower()

                        # Check if this page contains section headers
                        matches = 0
                        for section_type in section_types:
                            # Look for section headers or emphasized text
                            patterns = [
                                rf'\b{section_type}s?\b',  # Word boundary match
                                rf'{section_type}:',  # With colon
                                rf'^{section_type}',  # At start of line
                            ]

                            for pattern in patterns:
                                if re.search(pattern, lower_text, re.IGNORECASE | re.MULTILINE):
                                    matches += 1
                                    break

                        if matches > 0:
                            relevance = matches / len(section_types)
                            display_text = text[:context_chars] if len(text) > context_chars else text

                            results.append(PDFSearchResult(
                                pdf_name=pdf_name,
                                page_num=page_num,
                                text=display_text,
                                relevance_score=relevance
                            ))
            except Exception as e:
                print(f"Error searching {pdf_name}: {e}")
                continue

        results.sort(key=lambda x: x.relevance_score, reverse=True)
        return results[:max_results]

    def search_topic(
        self,
        topic_keywords: List[str],
        required_keywords: Optional[List[str]] = None,
        pdf_names: Optional[List[str]] = None,
        max_results: int = 5,
        context_chars: int = 2500
    ) -> List[PDFSearchResult]:
        """
        Search for pages related to a specific topic.

        Args:
            topic_keywords: Keywords related to the topic
            required_keywords: Keywords that must be present (e.g., for filtering)
            pdf_names: Optional list of PDF names to search
            max_results: Maximum results to return
            context_chars: Characters to include in results

        Returns:
            List of PDFSearchResult objects
        """
        results = []
        search_pdfs = pdf_names or list(self.pdf_paths.keys())

        for pdf_name in search_pdfs:
            pdf_path = self.pdf_paths.get(pdf_name)
            if not pdf_path or not pdf_path.exists():
                continue

            try:
                with open(pdf_path, 'rb') as f:
                    pdf = PyPDF2.PdfReader(f)

                    for page_num, page in enumerate(pdf.pages, start=1):
                        text = page.extract_text()
                        lower_text = text.lower()

                        # Check required keywords first
                        if required_keywords:
                            has_all_required = all(
                                kw.lower() in lower_text for kw in required_keywords
                            )
                            if not has_all_required:
                                continue

                        # Count topic keyword matches
                        topic_matches = sum(1 for kw in topic_keywords if kw.lower() in lower_text)

                        if topic_matches > 0:
                            relevance = topic_matches / len(topic_keywords)
                            display_text = text[:context_chars] if len(text) > context_chars else text

                            results.append(PDFSearchResult(
                                pdf_name=pdf_name,
                                page_num=page_num,
                                text=display_text,
                                relevance_score=relevance
                            ))
            except Exception as e:
                print(f"Error searching {pdf_name}: {e}")
                continue

        results.sort(key=lambda x: x.relevance_score, reverse=True)
        return results[:max_results]

    def get_page(self, pdf_name: str, page_num: int) -> Optional[str]:
        """
        Get the full text of a specific page.

        Args:
            pdf_name: Name of the PDF
            page_num: Page number (1-based)

        Returns:
            Page text or None if not found
        """
        pdf_path = self.pdf_paths.get(pdf_name)
        if not pdf_path or not pdf_path.exists():
            return None

        try:
            with open(pdf_path, 'rb') as f:
                pdf = PyPDF2.PdfReader(f)
                if page_num < 1 or page_num > len(pdf.pages):
                    return None
                return pdf.pages[page_num - 1].extract_text()
        except Exception as e:
            print(f"Error reading page {page_num} from {pdf_name}: {e}")
            return None

    def get_pages_range(self, pdf_name: str, start_page: int, end_page: int) -> List[Tuple[int, str]]:
        """
        Get text from a range of pages.

        Args:
            pdf_name: Name of the PDF
            start_page: Starting page number (1-based, inclusive)
            end_page: Ending page number (1-based, inclusive)

        Returns:
            List of tuples (page_number, page_text)
        """
        results = []
        pdf_path = self.pdf_paths.get(pdf_name)
        if not pdf_path or not pdf_path.exists():
            return results

        try:
            with open(pdf_path, 'rb') as f:
                pdf = PyPDF2.PdfReader(f)
                for page_num in range(start_page, min(end_page + 1, len(pdf.pages) + 1)):
                    text = pdf.pages[page_num - 1].extract_text()
                    results.append((page_num, text))
        except Exception as e:
            print(f"Error reading pages {start_page}-{end_page} from {pdf_name}: {e}")

        return results


def format_search_results(results: List[PDFSearchResult], max_chars_per_result: int = 1500) -> str:
    """
    Format search results for display.

    Args:
        results: List of PDFSearchResult objects
        max_chars_per_result: Maximum characters to show per result

    Returns:
        Formatted string
    """
    if not results:
        return "No results found."

    output = []
    output.append(f"Found {len(results)} relevant page(s):\n")

    for i, result in enumerate(results, start=1):
        output.append(f"{'=' * 80}")
        output.append(f"Result {i}: {result.pdf_name} - Page {result.page_num} (relevance: {result.relevance_score:.2f})")
        output.append(f"{'=' * 80}")
        text = result.text[:max_chars_per_result] if len(result.text) > max_chars_per_result else result.text
        output.append(text)
        output.append("")

    return "\n".join(output)
