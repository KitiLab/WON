#!/usr/bin/env python3
"""
tools/check_markdown_links.py

Authoritative Markdown link and image link validator for the WON repository.
Inspects Markdown files recursively, resolves relative paths, handles percent-encoding,
strips anchors, ignores code blocks and inline code, and reports broken targets with line numbers.
"""

import argparse
import os
import re
import sys
import urllib.parse
from pathlib import Path
from typing import Dict, List, NamedTuple, Optional, Tuple


class LinkFinding(NamedTuple):
    file_path: Path
    line_number: int
    text: str
    raw_target: str
    cleaned_target: str
    resolved_path: Path
    is_image: bool


class BrokenLink(NamedTuple):
    finding: LinkFinding
    reason: str


# Matches fenced code blocks (``` or ~~~)
FENCED_CODE_PATTERN = re.compile(
    r"(?ms)^[ ]{0,3}(`{3,}|~{3,})[^\n]*\n.*?\n[ ]{0,3}\1[ ]*$"
)

# Matches inline code spans (`...` or ``...``)
INLINE_CODE_PATTERN = re.compile(r"(`+)(.+?)\1", re.DOTALL)

# Matches standard markdown links [text](target) and images ![alt](target)
# Excludes links preceded by extra brackets or formatting artifacts
LINK_PATTERN = re.compile(
    r"(?P<is_image>!?)(?:\[(?P<text>[^\]]*)\])\((?P<raw_target>[^)\n]+)\)"
)

# Ignored directories
DEFAULT_EXCLUDE_DIRS = {
    ".git",
    ".gemini",
    ".idea",
    ".vscode",
    "node_modules",
    "vendor",
    "build",
    "bin",
}


def mask_string(match: re.Match) -> str:
    """
    Replaces matched text with spaces while preserving line breaks (\n).
    This keeps character offsets and line numbers identical to the original text.
    """
    text = match.group(0)
    return "".join("\n" if ch == "\n" else " " for ch in text)


def strip_code(content: str) -> str:
    """
    Strips fenced code blocks and inline code spans by masking them with spaces.
    """
    # First mask fenced code blocks
    content_no_fenced = FENCED_CODE_PATTERN.sub(mask_string, content)
    # Then mask inline code spans
    content_clean = INLINE_CODE_PATTERN.sub(mask_string, content_no_fenced)
    return content_clean


def parse_target(raw_target: str) -> Tuple[str, Optional[str]]:
    """
    Parses a raw link target, extracting the target path/URL and optional title.
    Handles angle-bracketed paths e.g. <path/to/file> and titles e.g. "title".
    """
    target = raw_target.strip()
    title = None

    if target.startswith("<") and ">" in target:
        end_idx = target.find(">")
        path_part = target[1:end_idx].strip()
        title_part = target[end_idx + 1:].strip()
        if title_part:
            title = title_part.strip("\"'")
        return path_part, title

    # Check for optional title separated by whitespace e.g. path/to/file.md "Title"
    parts = target.split(None, 1)
    if len(parts) == 2:
        path_part, title_part = parts[0], parts[1].strip()
        if (title_part.startswith('"') and title_part.endswith('"')) or \
           (title_part.startswith("'") and title_part.endswith("'")):
            title = title_part[1:-1]
            return path_part, title

    return target, None


def is_external_or_anchor(target: str) -> bool:
    """
    Returns True if target is an external URL (http, https, mailto, etc.)
    or a fragment-only internal anchor (#...).
    """
    if not target or target.startswith("#"):
        return True
    lower = target.lower()
    return (
        lower.startswith("http://")
        or lower.startswith("https://")
        or lower.startswith("mailto:")
        or lower.startswith("ftp://")
        or lower.startswith("file://")
    )


def extract_links_from_file(file_path: Path) -> List[LinkFinding]:
    """
    Extracts all relative links and images from a single markdown file.
    """
    try:
        content = file_path.read_text(encoding="utf-8")
    except UnicodeDecodeError:
        content = file_path.read_text(encoding="utf-8", errors="replace")

    clean_content = strip_code(content)
    findings: List[LinkFinding] = []

    # Map character offsets to line numbers
    newline_offsets = [0]
    for idx, char in enumerate(content):
        if char == "\n":
            newline_offsets.append(idx + 1)

    def get_line_number(offset: int) -> int:
        # Binary search or bisect can be used; linear search is fast enough for typical docs
        import bisect
        return bisect.bisect_right(newline_offsets, offset)

    for m in LINK_PATTERN.finditer(clean_content):
        raw_target = m.group("raw_target")
        is_image = m.group("is_image") == "!"
        text = m.group("text")
        start_offset = m.start()
        line_num = get_line_number(start_offset)

        target_path_str, _ = parse_target(raw_target)
        if is_external_or_anchor(target_path_str):
            continue

        clean_path = target_path_str.split("#")[0]
        if not clean_path:
            # Anchor only after stripping
            continue

        decoded_path = urllib.parse.unquote(clean_path)
        # Resolve target path relative to the file directory
        resolved_path = (file_path.parent / decoded_path).resolve()

        findings.append(
            LinkFinding(
                file_path=file_path,
                line_number=line_num,
                text=text,
                raw_target=raw_target,
                cleaned_target=decoded_path,
                resolved_path=resolved_path,
                is_image=is_image,
            )
        )

    return findings


def check_markdown_links(
    root_dir: Path,
    exclude_dirs: Optional[set] = None,
    verbose: bool = False,
) -> Tuple[int, int, List[BrokenLink]]:
    """
    Checks all markdown links in root_dir recursively.
    Returns (num_files_checked, num_links_checked, broken_links_list).
    """
    if exclude_dirs is None:
        exclude_dirs = DEFAULT_EXCLUDE_DIRS

    md_files: List[Path] = []
    for dirpath, dirnames, filenames in os.walk(root_dir):
        # Exclude directories in-place
        dirnames[:] = [d for d in dirnames if d not in exclude_dirs]
        for f in filenames:
            if f.endswith(".md"):
                md_files.append(Path(dirpath) / f)

    md_files.sort()
    all_findings: List[LinkFinding] = []
    broken_links: List[BrokenLink] = []

    for file_path in md_files:
        file_findings = extract_links_from_file(file_path)
        all_findings.extend(file_findings)
        for finding in file_findings:
            if not finding.resolved_path.exists():
                broken_links.append(
                    BrokenLink(
                        finding=finding,
                        reason="File does not exist on disk",
                    )
                )

    return len(md_files), len(all_findings), broken_links


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Verify relative Markdown links across repository."
    )
    parser.add_argument(
        "--root",
        type=str,
        default=".",
        help="Root directory to check (default: current directory)",
    )
    parser.add_argument(
        "--verbose",
        "-v",
        action="store_true",
        help="Enable verbose output",
    )
    args = parser.parse_args()

    root_dir = Path(args.root).resolve()
    if not root_dir.exists():
        print(f"Error: Root directory '{root_dir}' does not exist.", file=sys.stderr)
        return 2

    files_checked, links_checked, broken = check_markdown_links(
        root_dir=root_dir,
        verbose=args.verbose,
    )

    print(f"Markdown Link Validation Report:")
    print(f"  Root: {root_dir}")
    print(f"  Markdown files checked: {files_checked}")
    print(f"  Relative links checked: {links_checked}")
    print(f"  Broken links detected:  {len(broken)}")

    if broken:
        print("\nBroken Link Details:")
        for b in broken:
            f = b.finding
            try:
                rel_file = f.file_path.relative_to(root_dir)
            except ValueError:
                rel_file = f.file_path
            print(
                f"  {rel_file}:{f.line_number} -> '{f.raw_target}' "
                f"(resolved: {f.resolved_path}) [{b.reason}]"
            )
        return 1

    print("\nSUCCESS: All relative Markdown links are valid.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
