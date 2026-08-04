#!/usr/bin/env python3
"""
scripts/roles.py — syntactic-role classification for text occurrences.

Mechanizes the mass-substitution rule: before any substitution,
classify every occurrence of the target by its syntactic role; a single
pass is safe only when all occurrences share one role and one correct
treatment. This module answers "what roles does this text appear in?"
so that judgment starts from facts.

Importable (ed.py uses classify()) and a CLI auditor:

    python3 scripts/roles.py <term> [path ...]

prints every occurrence with its role. Roles are HEURISTIC and
advisory — they inform the classification step, they do not replace it.

Role vocabulary:
  go-backtick-string | go-dquote-string | go-comment | go-code
  md-fence | md-inline-code | md-table | md-heading | md-prose
  text
"""

import re
import sys
from pathlib import Path


def _go_role(text: str, offset: int) -> str:
    """Scan the line-local context; delimiter state tracked from the
    start of the enclosing line (Go string literals cannot span lines
    except backticks — for those, scan back to the opening backtick)."""
    # Backtick strings can span lines: count backticks before offset.
    if text[:offset].count("`") % 2 == 1:
        return "go-backtick-string"
    line_start = text.rfind("\n", 0, offset) + 1
    line = text[line_start:offset]
    # Comment?
    if "//" in _strip_go_strings(line):
        return "go-comment"
    # Double-quoted string state within the line.
    in_dq = False
    i = 0
    while i < len(line):
        c = line[i]
        if c == "\\" and in_dq:
            i += 2
            continue
        if c == '"':
            in_dq = not in_dq
        i += 1
    if in_dq:
        return "go-dquote-string"
    # Block comments: crude but honest — count openers/closers.
    before = text[:offset]
    if before.count("/*") > before.count("*/"):
        return "go-comment"
    return "go-code"


def _strip_go_strings(line: str) -> str:
    out, in_dq, i = [], False, 0
    while i < len(line):
        c = line[i]
        if c == "\\" and in_dq:
            i += 2
            continue
        if c == '"':
            in_dq = not in_dq
            i += 1
            continue
        if not in_dq:
            out.append(c)
        i += 1
    return "".join(out)


def _md_role(text: str, offset: int) -> str:
    before = text[:offset]
    # Fenced block: odd number of ``` fences before us.
    if len(re.findall(r"^```", before, re.M)) % 2 == 1:
        return "md-fence"
    line_start = before.rfind("\n") + 1
    line_end = text.find("\n", offset)
    line = text[line_start:line_end if line_end != -1 else len(text)]
    prefix = text[line_start:offset]
    if prefix.count("`") % 2 == 1:
        return "md-inline-code"
    if line.lstrip().startswith("|"):
        return "md-table"
    if line.lstrip().startswith("#"):
        return "md-heading"
    return "md-prose"


def classify(path: Path, text: str, offset: int) -> str:
    """Role of the occurrence starting at byte offset in text."""
    suffix = path.suffix.lower()
    if suffix == ".go":
        return _go_role(text, offset)
    if suffix in (".md", ".markdown"):
        return _md_role(text, offset)
    return "text"


def occurrences(term: str, paths, regex: bool = False):
    """Yield (path, offset, end, role, line_no, line_text) for every
    occurrence of term in the given files."""
    pat = re.compile(term if regex else re.escape(term))
    for p in paths:
        p = Path(p)
        try:
            text = p.read_text()
        except (UnicodeDecodeError, OSError):
            continue
        for m in pat.finditer(text):
            line_no = text.count("\n", 0, m.start()) + 1
            ls = text.rfind("\n", 0, m.start()) + 1
            le = text.find("\n", m.start())
            line = text[ls:le if le != -1 else len(text)]
            yield p, m.start(), m.end(), classify(p, text, m.start()), line_no, line


def expand(paths):
    out = []
    for p in paths:
        p = Path(p)
        if p.is_dir():
            out += [f for f in sorted(p.rglob("*"))
                    if f.is_file() and ".git" not in f.parts]
        elif p.is_file():
            out.append(p)
    return out


def main(argv) -> int:
    if len(argv) < 1:
        print(__doc__)
        return 1
    term, paths = argv[0], expand(argv[1:] or ["."])
    by_role = {}
    for p, s, e, role, ln, line in occurrences(term, paths):
        by_role.setdefault(role, []).append((p, ln, line))
        print(f"{p}:{ln}: [{role}] {line.strip()[:90]}")
    if by_role:
        print(f"\nroles present: {sorted(by_role)} "
              f"({sum(len(v) for v in by_role.values())} occurrence(s))")
        if len(by_role) > 1:
            print("MULTIPLE ROLES: a single substitution pass is NOT safe; "
                  "write one targeted pass per role.")
    else:
        print("no occurrences")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
