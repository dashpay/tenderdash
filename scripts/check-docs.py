#!/usr/bin/env python3
"""Reject missing local pages and assets in an mdBook HTML build."""

from html.parser import HTMLParser
from pathlib import Path
import sys
from urllib.parse import unquote, urlsplit


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.targets = []

    def handle_starttag(self, tag, attributes):
        attributes = dict(attributes)
        if tag in ("a", "img", "script", "link"):
            target = attributes.get("href") or attributes.get("src")
            if target:
                self.targets.append(target)


def check(root):
    missing = set()
    pages = list(root.rglob("*.html"))
    for page in pages:
        # mdBook assembles the print view from chapters with different base paths.
        if page.name == "print.html":
            continue
        parser = Links()
        parser.feed(page.read_text())
        for target in parser.targets:
            url = urlsplit(target)
            if url.scheme or url.netloc or not url.path:
                continue
            path = unquote(url.path)
            resolved = (
                root / path.lstrip("/") if path.startswith("/") else page.parent / path
            ).resolve()
            if resolved.is_dir():
                resolved /= "index.html"
            if not resolved.is_relative_to(root) or not resolved.is_file():
                missing.add((str(page.relative_to(root)), target))
    if not pages:
        raise ValueError(f"No HTML pages found in {root}")
    if missing:
        for page, target in sorted(missing):
            print(f"{page}: missing local target {target}", file=sys.stderr)
        raise ValueError(f"{len(missing)} missing local references")
    print(f"Checked local links and assets in {len(pages)} HTML pages")


if __name__ == "__main__":
    check(Path(sys.argv[1]).resolve())
