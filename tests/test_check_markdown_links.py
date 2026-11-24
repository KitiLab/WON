#!/usr/bin/env python3
"""
tests/test_check_markdown_links.py

Comprehensive regression test suite for tools/check_markdown_links.py.
Validates:
1. Valid relative Markdown links
2. Valid relative image links
3. Valid ../ references
4. Valid percent-encoded filenames
5. Fragment-only links (ignored)
6. External links (http, https, mailto - ignored)
7. Links with optional titles
8. Broken relative links (detected as errors)
9. Markdown-looking text inside fenced code blocks (ignored)
10. Markdown-looking text inside inline code (ignored)
"""

import sys
import tempfile
import unittest
from pathlib import Path

# Add repo root to import path
REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT))

from tools.check_markdown_links import (
    BrokenLink,
    check_markdown_links,
    extract_links_from_file,
    strip_code,
)


class TestMarkdownLinkChecker(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)

    def tearDown(self):
        self.temp_dir.cleanup()

    def test_strip_code_fenced(self):
        content = "Line 1\n```won\nfn swap[T](a: &mut T, b: &mut T)\n```\nLine 5"
        stripped = strip_code(content)
        self.assertNotIn("swap[T]", stripped)
        self.assertIn("Line 1", stripped)
        self.assertIn("Line 5", stripped)
        # Line count preserved
        self.assertEqual(content.count("\n"), stripped.count("\n"))

    def test_strip_code_inline(self):
        content = "Here is `[T](arg)` inline code."
        stripped = strip_code(content)
        self.assertNotIn("[T](arg)", stripped)
        self.assertIn("Here is", stripped)
        self.assertEqual(len(content), len(stripped))

    def test_valid_relative_links_and_images(self):
        # Create target files
        doc1 = self.root / "doc1.md"
        sub = self.root / "sub"
        sub.mkdir()
        doc2 = sub / "doc2.md"
        img = sub / "diagram.png"

        doc2.write_text("# Doc 2\n", encoding="utf-8")
        img.write_bytes(b"\x89PNG\r\n\x1a\n")

        doc1.write_text(
            "# Doc 1\n"
            "See [Sub Doc](sub/doc2.md).\n"
            "See image: ![Diagram](sub/diagram.png).\n"
            "Anchor link: [Section](#section).\n"
            "External: [GitHub](https://github.com/KitiLab/WON).\n"
            "Mailto: [Contact](mailto:eng@kitilab.com).\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 2)
        self.assertEqual(links, 2)  # sub/doc2.md and sub/diagram.png
        self.assertEqual(len(broken), 0)

    def test_valid_parent_and_percent_encoded_links(self):
        sub = self.root / "sub"
        sub.mkdir()
        child = sub / "child.md"
        parent_target = self.root / "my file with spaces.md"
        parent_target.write_text("# Parent\n", encoding="utf-8")

        child.write_text(
            "# Child\n"
            "Link to parent with ../: [Parent](../my%20file%20with%20spaces.md).\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 2)
        self.assertEqual(links, 1)
        self.assertEqual(len(broken), 0)

    def test_links_with_optional_titles(self):
        target = self.root / "target.md"
        target.write_text("# Target\n", encoding="utf-8")

        source = self.root / "source.md"
        source.write_text(
            '# Source\n'
            '[Link Title Double](target.md "Optional Title")\n'
            "[Link Title Single](target.md 'Another Title')\n"
            '[Link Angle](<target.md> "Angle Title")\n',
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 2)
        self.assertEqual(links, 3)
        self.assertEqual(len(broken), 0)

    def test_broken_relative_links_detected(self):
        source = self.root / "source.md"
        source.write_text(
            "# Source\n"
            "This link is broken: [Non Existent](does_not_exist.md).\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 1)
        self.assertEqual(links, 1)
        self.assertEqual(len(broken), 1)
        self.assertEqual(broken[0].finding.cleaned_target, "does_not_exist.md")
        self.assertEqual(broken[0].finding.line_number, 2)

    def test_generic_syntax_in_fenced_code_not_broken_link(self):
        source = self.root / "source.md"
        source.write_text(
            "# Source\n"
            "Here is code:\n"
            "```won\n"
            "fn swap[T](a: &mut T, b: &mut T):\n"
            "    pass\n"
            "let ch = Channel::bounded[i32](capacity: 100)\n"
            "```\n"
            "And here is valid text.\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 1)
        self.assertEqual(links, 0)
        self.assertEqual(len(broken), 0)

    def test_generic_syntax_in_inline_code_not_broken_link(self):
        source = self.root / "source.md"
        source.write_text(
            "# Source\n"
            "Use `Channel::bounded[i32](capacity: 100)` to create a channel.\n"
            "Also `items: &[T]` is a slice.\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(files, 1)
        self.assertEqual(links, 0)
        self.assertEqual(len(broken), 0)

    def test_line_number_accuracy_with_code_blocks(self):
        source = self.root / "source.md"
        source.write_text(
            "Line 1\n"
            "```won\n"
            "fn foo[T](x: T) -> T\n"
            "```\n"
            "Line 5: [Broken](missing.md)\n",
            encoding="utf-8",
        )

        files, links, broken = check_markdown_links(self.root)
        self.assertEqual(len(broken), 1)
        self.assertEqual(broken[0].finding.line_number, 5)


if __name__ == "__main__":
    unittest.main()
