#!/usr/bin/env python3
"""Stage canonical docs and specifications for mdBook; never edit the sources."""

import argparse
import os
from pathlib import Path
import re
import shutil
from urllib.parse import quote, unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
LINK = re.compile(r"(!?\[[^\]]*\]\(|^\[[^\]\n]+\]:\s*)(<?[^\s)>]+>?)", re.MULTILINE)
IGNORED = {"node_modules", ".vuepress", "_build", "dist", "__pycache__"}
FRONT_MATTER = re.compile(r"\A---\n.*?\n---(?:\n|\Z)", re.DOTALL)


def ignored_in(folder):
    return IGNORED | ({"spec"} if folder == "docs" else set())


def rewrite_link(match, source, root, source_ref):
    prefix, target = match.groups()
    url = urlsplit(target.strip("<>"))
    if url.scheme or url.netloc or not url.path:
        return match.group(0)
    path = unquote(url.path)
    resolved = (
        root / path.lstrip("/") if path.startswith("/") else source.parent / path
    ).resolve()
    if not resolved.is_relative_to(root):
        return match.group(0)
    if resolved.is_dir():
        for index in ("README.md", "readme.md", "index.md"):
            if (resolved / index).is_file():
                resolved /= index
                break
    relative = resolved.relative_to(root)
    if relative.parts[0] in ("docs", "spec"):
        replacement = os.path.relpath(resolved, source.parent)
        if resolved.name.lower() == "readme.md":
            replacement = str(Path(replacement).with_name("index.md"))
    elif resolved.exists():
        kind = "tree" if resolved.is_dir() else "blob"
        replacement = f"https://github.com/dashpay/tenderdash/{kind}/{quote(source_ref, safe='')}/{quote(str(relative))}"
    else:
        return match.group(0)
    if url.query:
        replacement += "?" + url.query
    if url.fragment:
        replacement += "#" + url.fragment
    return prefix + replacement


def prepare(root=ROOT, source_ref="v1.8-dev"):
    root = root.resolve()
    summary = (root / "book/SUMMARY.md").read_text()
    listed = set(re.findall(r"\]\(([^)]+\.md)\)", summary))
    chapters = {
        str(path.relative_to(root))
        for folder in ("docs", "spec")
        for path in (root / folder).rglob("*.md")
        if not ignored_in(folder).intersection(path.relative_to(root / folder).parts)
    }
    listed.discard("README.md")
    if listed != chapters:
        raise ValueError(
            f"Update book/SUMMARY.md: unlisted={sorted(chapters - listed)}, missing={sorted(listed - chapters)}"
        )
    destination = root / "book/src"
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True)
    for folder in ("docs", "spec"):
        shutil.copytree(
            root / folder,
            destination / folder,
            ignore=shutil.ignore_patterns(*ignored_in(folder)),
        )
    shutil.copytree(root / "rpc/openapi", destination / "api/rpc")
    for chapter in chapters:
        source = root / chapter
        content = FRONT_MATTER.sub("", source.read_text())
        content = LINK.sub(
            lambda match: rewrite_link(match, source, root, source_ref), content
        )
        (destination / chapter).write_text(content)
    introduction = (root / "book/README.md").read_text()
    introduction = LINK.sub(
        lambda match: rewrite_link(match, root / "README.md", root, source_ref),
        introduction,
    )
    shutil.copyfile(root / "book/mermaid-LICENSE", destination / "mermaid-LICENSE")
    (destination / "README.md").write_text(introduction)
    (destination / "SUMMARY.md").write_text(summary)
    print(f"Staged {len(chapters)} chapters in {destination}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-ref",
        default="v1.8-dev",
        help="Git ref for links to repository source files",
    )
    args = parser.parse_args()
    prepare(source_ref=args.source_ref)
