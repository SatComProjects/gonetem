#!/usr/bin/env python3
"""Validate the local Markdown documentation.

Checks:
* no reStructuredText/Sphinx source or configuration remains;
* every page has exactly one H1 and is reachable from docs/README.md;
* relative links and anchors resolve;
* fenced code blocks and pipe tables are well formed;
* the documented version matches VERSION and the compiled default.

Run from the repository root: python3 scripts/check-docs.py
"""

from __future__ import annotations

import re
import sys
import urllib.parse
from collections import deque
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
ENTRY = DOCS / "README.md"
ROOT_PAGES = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "CONTRIBUTING.md"]

FENCE_RE = re.compile(r"^(?P<indent>\s*)(?P<fence>`{3,}|~{3,})(?P<info>.*)$")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
LINK_RE = re.compile(r"!?\[[^\]]*\]\((?P<target>[^)]+)\)")
INLINE_CODE_RE = re.compile(r"`[^`]*`")
SPHINX_RE = re.compile(
    r":(?:ref|doc|numref|class|func|meth|attr):|^\s*\.\. [A-Za-z-]+::|"
    r"\btoctree\b|\bSphinx\b|Read the Docs|<(?:div|span)\s+class=",
    re.MULTILINE,
)

errors: list[str] = []


def fail(message: str) -> None:
    errors.append(message)


def strip_code(text: str) -> str:
    return INLINE_CODE_RE.sub(lambda m: " " * len(m.group(0)), text)


def headings(text: str) -> list[tuple[int, str, str]]:
    """Return (level, title, slug) for headings outside fenced blocks."""
    found: list[tuple[int, str, str]] = []
    seen: dict[str, int] = {}
    fence: str | None = None
    for line in text.splitlines():
        match = FENCE_RE.match(line)
        if match:
            marker = match.group("fence")
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        match = HEADING_RE.match(line)
        if not match:
            continue
        title = strip_code(match.group(2)).strip()
        plain = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", title)
        plain = re.sub(r"[*_~]", "", plain)
        slug = re.sub(r"[^\w\s-]", "", plain.lower(), flags=re.UNICODE)
        slug = re.sub(r"\s+", "-", slug).strip("-")
        count = seen.get(slug, 0)
        seen[slug] = count + 1
        if count:
            slug = f"{slug}-{count}"
        found.append((len(match.group(1)), plain, slug))
    return found


def check_fences(path: Path, text: str) -> None:
    fence: str | None = None
    for number, line in enumerate(text.splitlines(), 1):
        match = FENCE_RE.match(line)
        if not match:
            continue
        marker = match.group("fence")
        if fence is None:
            fence = marker
        elif marker[0] == fence[0] and len(marker) >= len(fence):
            fence = None
    if fence is not None:
        fail(f"{path.relative_to(ROOT)}: unclosed code fence")


def check_tables(path: Path, text: str) -> None:
    fence: str | None = None
    block: list[tuple[int, str]] = []
    for number, line in enumerate(text.splitlines() + [""], 1):
        match = FENCE_RE.match(line)
        if match:
            marker = match.group("fence")
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
        if fence is not None:
            continue
        if line.lstrip().startswith("|"):
            block.append((number, line))
            continue
        if len(block) >= 2:
            counts = []
            for _, row in block:
                pipes = 0
                escaped = False
                for char in row:
                    if escaped:
                        escaped = False
                        continue
                    if char == "\\":
                        escaped = True
                    elif char == "|":
                        pipes += 1
                counts.append(pipes)
            if len(set(counts)) != 1:
                fail(
                    f"{path.relative_to(ROOT)}:{block[0][0]}: "
                    f"table rows have inconsistent column counts {counts}"
                )
        block = []


def parse_targets(text: str) -> list[str]:
    targets: list[str] = []
    fence: str | None = None
    for line in text.splitlines():
        match = FENCE_RE.match(line)
        if match:
            marker = match.group("fence")
            if fence is None:
                fence = marker
            elif marker[0] == fence[0] and len(marker) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        for link in LINK_RE.finditer(strip_code(line)):
            target = link.group("target").strip()
            if target.startswith("<") and target.endswith(">"):
                target = target[1:-1]
            target = target.split(' "', 1)[0].strip()
            targets.append(target)
    return targets


def anchor_map(path: Path) -> set[str]:
    text = path.read_text(encoding="utf-8")
    return {slug for _, _, slug in headings(text)} | {"top"}


def check_links(path: Path) -> list[Path]:
    text = path.read_text(encoding="utf-8")
    linked_pages: list[Path] = []
    for target in parse_targets(text):
        if not target or target.startswith(("http://", "https://", "mailto:")):
            continue
        target = urllib.parse.unquote(target)
        if target.startswith("#"):
            if target[1:] not in anchor_map(path):
                fail(f"{path.relative_to(ROOT)}: missing anchor {target}")
            continue
        file_part, _, anchor = target.partition("#")
        resolved = (path.parent / file_part).resolve()
        try:
            resolved.relative_to(ROOT)
        except ValueError:
            fail(f"{path.relative_to(ROOT)}: link escapes the repository: {target}")
            continue
        if not resolved.exists():
            fail(f"{path.relative_to(ROOT)}: broken link {target}")
            continue
        if resolved.suffix == ".md":
            linked_pages.append(resolved)
            if anchor and anchor not in anchor_map(resolved):
                fail(f"{path.relative_to(ROOT)}: missing anchor {target}")
    return linked_pages


def main() -> int:
    if not ENTRY.exists():
        fail("docs/README.md is missing")
        print_errors()
        return 1

    for stale in ("docs/source", "docs/Makefile", "docs/requirements.txt", "docs/conf.py"):
        if (ROOT / stale).exists():
            fail(f"{stale} still exists; the documentation is now local Markdown")
    if (ROOT / ".readthedocs.yaml").exists():
        fail(".readthedocs.yaml still exists; the documentation is now local Markdown")

    rst = sorted(DOCS.rglob("*.rst"))
    if rst:
        fail("reStructuredText files remain: " + ", ".join(str(p.relative_to(ROOT)) for p in rst))

    pages = sorted(DOCS.rglob("*.md"))
    if not pages:
        fail("no Markdown pages under docs/")
    for page in pages:
        text = page.read_text(encoding="utf-8")
        levels = headings(text)
        h1 = [item for item in levels if item[0] == 1]
        if len(h1) != 1:
            fail(f"{page.relative_to(ROOT)}: expected exactly one H1, found {len(h1)}")
        if levels and levels[0][0] != 1:
            fail(f"{page.relative_to(ROOT)}: first heading is not H1")
        if SPHINX_RE.search(text):
            fail(f"{page.relative_to(ROOT)}: Sphinx/reStructuredText markup remains")
        if re.search(r"\bdocs/source\b|\.readthedocs|\.rst\b", text):
            fail(f"{page.relative_to(ROOT)}: stale reStructuredText path or extension")
        check_fences(page, text)
        check_tables(page, text)

    # Reachability from the documentation index.
    visited: set[Path] = {ENTRY.resolve()}
    queue = deque([ENTRY])
    while queue:
        current = queue.popleft()
        for linked in check_links(current):
            if linked.suffix == ".md" and DOCS in linked.parents:
                if linked not in visited:
                    visited.add(linked)
                    queue.append(linked)
    for page in pages:
        if page.resolve() not in visited:
            fail(f"{page.relative_to(ROOT)}: page is not reachable from docs/README.md")

    # Root-level documentation links.
    for page in ROOT_PAGES:
        if not page.exists():
            continue
        text = page.read_text(encoding="utf-8")
        if re.search(r"\bdocs/source\b|\.readthedocs|\bSphinx\b", text):
            fail(f"{page.name}: stale documentation reference")
        check_links(page)

    # Version consistency.
    version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    readme = ENTRY.read_text(encoding="utf-8")
    match = re.search(r"Version documented here:\s*\*\*([^*]+)\*\*", readme)
    if not match:
        fail("docs/README.md: missing 'Version documented here: **x.y.z**'")
    elif match.group(1).strip() != version:
        fail(f"docs/README.md documents {match.group(1)}, VERSION is {version}")
    server_go = (ROOT / "internal/options/server.go").read_text(encoding="utf-8")
    match = re.search(r'var VERSION = "([^"]+)"', server_go)
    if not match:
        fail("internal/options/server.go: VERSION default not found")
    elif match.group(1) != version:
        fail(f"internal/options/server.go compiles {match.group(1)}, VERSION is {version}")

    print_errors()
    if errors:
        return 1
    print(f"docs OK: {len(pages)} pages, version {version}")
    return 0


def print_errors() -> None:
    for error in errors:
        print(f"error: {error}", file=sys.stderr)


if __name__ == "__main__":
    raise SystemExit(main())
