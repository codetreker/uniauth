#!/usr/bin/env python3
"""Validate active document links and the shared workflow lifecycle."""

from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent
paths = [ROOT / "AGENTS.md", *ROOT.glob("*.md")]
paths += list((ROOT / "docs").rglob("*.md"))
paths += list((ROOT / ".agents").rglob("*.md"))
checked = set()

for path in paths:
    if "archived" in path.parts or "plans" in path.parts or path.is_symlink():
        continue
    if path in checked:
        continue
    checked.add(path)
    text = path.read_text()
    assert text.count("```") % 2 == 0, f"Unclosed code fence: {path}"
    for target in re.findall(r"\[[^\]]*\]\(([^)]+)\)", text):
        if target.startswith(("https://", "http://", "#", "mailto:")):
            continue
        location = target.split("#", 1)[0]
        assert (path.parent / location).exists(), f"Broken link: {path}: {target}"
    if path.name == "SKILL.md":
        frontmatter = re.match(r"^---\n(.*?)\n---\n", text, re.S)
        assert frontmatter, f"Missing skill metadata: {path}"
        assert re.search(r"^name: " + re.escape(path.parent.name) + r"$", frontmatter[1], re.M), path
        assert re.search(r"^description: .+", frontmatter[1], re.M), path
    if ".agents/notes" in path.as_posix() and path.name not in ("README.md", "AGENTS.md"):
        lifecycle = path.parent.parent.name
        status = "rejected - " if lifecycle == "rejected" else lifecycle
        assert f"Status: {status}" in text, f"Status mismatch: {path}"
        sections = ("Problem", "Decision", "Alternatives", "Consequences") if lifecycle == "implemented" else ("Problem", "Proposal", "Alternatives", "Acceptance Criteria", "Risks")
        for section in sections:
            assert f"## {section}" in text, f"Missing {section}: {path}"

assert (ROOT / "CLAUDE.md").is_symlink()
assert (ROOT / "CLAUDE.md").resolve() == ROOT / "AGENTS.md"
assert (ROOT / ".claude/skills").is_symlink()
assert (ROOT / ".claude/skills").resolve() == ROOT / ".agents/skills"
print(f"Checked {len(checked)} active documents, skill metadata, note lifecycle and discovery aliases.")
