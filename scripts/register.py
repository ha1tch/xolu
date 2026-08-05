#!/usr/bin/env python3
"""
scripts/register.py — mechanized operations on the live register
(docs/TRACKING.md) and resolution record (docs/RESOLVED.md).

The closure procedure (TRACKING_PRACTICES.md, "Closure procedure") is
precisely specified and repeatedly hand-executed — which is exactly
where hand-editing mistakes live. This tool makes the procedure's
violations unmakeable rather than detected after the fact.

Commands:

    list                       one line per open item
    show T-nn                  print an item's detail section
    add  --summary S --theme T --priority Pn [--status ☐]
         [--blocks TEXT] [--body TEXT | --body-file F] [--dry-run]
                               file a new item: next free id, row +
                               detail section inserted together
    close T-nn --version X.Y.Z [--date YYYY-MM-DD] [--dry-run]
                               closure procedure: detail text moved to
                               the top of RESOLVED.md stamped with
                               version+date; row AND detail removed
                               from the register in the same operation
    check                      register consistency (the release gate's
                               A1–A3 delegate here — one implementation,
                               so the gate and this editor cannot
                               disagree about what consistent means)

Always run `check` (or the release gate) after any manual edit to the
register. `--dry-run` prints what would change without writing.

Status symbols: ✓ done · ◐ partial · ☐ not started · ✗ dropped.
A ✓ item in the register is itself a defect — close it instead.
"""

import argparse
import datetime
import difflib
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TRACKING = ROOT / "docs" / "TRACKING.md"
RESOLVED = ROOT / "docs" / "RESOLVED.md"

# NEW_PREFIX: forward-only project prefix for new register items, added
# 2026-08-04 -- Horacio's own decision, directly motivated by a real
# collision found while syncing repoman upstream (T-163): a closure
# narrative in a DIFFERENT project's own register (seam-ui) was
# confused by xolu's own bare "T-160"/"T-161" mentioned in prose,
# jumping their own next_id() sequence. A per-project prefix
# (xolu="XOT", xoluman="XMT", seam="SET") makes that structurally
# impossible instead of merely unlikely -- an ID from another project
# can never collide with or be mistaken for one of xolu's own,
# regardless of what free-form prose quotes it.
#
# Deliberately forward-only, not retroactive: this project's own "IDs
# are never reused and never renumbered" rule treats a full ID string
# as permanent once assigned. T-1 through T-163 keep their original,
# unprefixed form forever, exactly as filed; only T-164 onward uses the
# new "XOT<n>" shape (no hyphen, matching the literal example given
# when this was decided). Both shapes are matched throughout this file
# so the existing history and new items are read, sorted, and
# cross-checked together -- neither format is ever assumed exclusively.
NEW_PREFIX = "XOT"

STATUS_SYMBOLS = {"✓", "◐", "☐", "✗"}
_ID_ALT = r"(?:T-|" + re.escape(NEW_PREFIX) + r")\d+"
ID_RE = re.compile(_ID_ALT)
ROW_RE = re.compile(r"^\| (" + _ID_ALT + r") \|")
HEAD_RE = re.compile(r"^### (" + _ID_ALT + r")\. (.*)$")
FIELD_RE = re.compile(
    r"^Theme: (\S+) · Priority: \*{0,2}(P\d)\*{0,2} · Status: (\S+)(?: · Blocks/after: (.*))?$",
    re.M)


def _id_num(tid: str) -> int:
    """The numeric portion of an id, old ("T-NNN") or new ("XOTNNN")
    format -- the two prefixes differ in length (2 chars vs. 3, and
    only one carries a hyphen), so a fixed-offset slice like the old
    t[2:] is wrong for one of them. Raises ValueError on anything
    matching neither shape, rather than silently returning a wrong
    number."""
    if tid.startswith("T-"):
        return int(tid[2:])
    if tid.startswith(NEW_PREFIX):
        return int(tid[len(NEW_PREFIX):])
    raise ValueError(f"unrecognized id format: {tid!r}")


class Item:
    def __init__(self, tid, title, theme, priority, status, blocks, body):
        self.tid, self.title = tid, title
        self.theme, self.priority, self.status = theme, priority, status
        self.blocks = blocks or ""
        self.body = body  # detail text below the field line, verbatim

    def field_line(self):
        s = f"Theme: {self.theme} · Priority: {self.priority} · Status: {self.status}"
        if self.blocks:
            s += f" · Blocks/after: {self.blocks}"
        return s


class Register:
    """Parsed view of TRACKING.md. parse() and render-back are
    edit-in-place on the original text (surgical splices), so untouched
    content — including formatting this parser does not model — is
    preserved byte-for-byte."""

    def __init__(self, text: str):
        self.text = text
        self.rows = {}     # tid -> (theme, pri, status, raw row line)
        self.items = {}    # tid -> Item
        self.spans = {}    # tid -> (start, end) offsets of the detail section
        self._parse()

    def _parse(self):
        for m in re.finditer(r"^\| (" + _ID_ALT + r") \|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|.*$",
                             self.text, re.M):
            self.rows[m.group(1)] = (m.group(3).strip(),
                                     m.group(4).strip().strip('*'),
                                     m.group(5).strip(), m.group(0))
        heads = list(re.finditer(r"^### (" + _ID_ALT + r")\. (.*)$", self.text, re.M))
        for i, h in enumerate(heads):
            start = h.start()
            # section ends at the next ### / ## heading or trailing ---
            tail = self.text[h.end():]
            nm = re.search(r"^(### |## |---\s*$)", tail, re.M)
            end = h.end() + (nm.start() if nm else len(tail))
            block = self.text[start:end]
            fm = FIELD_RE.search(block)
            if not fm:
                continue
            body_start = block.index(fm.group(0)) + len(fm.group(0))
            self.items[h.group(1)] = Item(
                h.group(1), h.group(2).strip(), fm.group(1), fm.group(2),
                fm.group(3), fm.group(4), block[body_start:].rstrip() + "\n")
            self.spans[h.group(1)] = (start, end)

    def next_id(self) -> str:
        ids = [_id_num(t) for t in set(self.rows) | set(self.items)]
        # BUGFIX (found 2026-07-29): IDs must never be reused, including
        # a closed item's — but this only ever scanned TRACKING.md's
        # currently-open items. Closing an item removes it from
        # TRACKING.md entirely, so the very next `add` after a `close`
        # in the same session would compute max() over a set missing
        # the ID just freed by that closure, and hand it straight back
        # out — a real collision with RESOLVED.md's permanent record,
        # not a theoretical one (T-82 was assigned to two different
        # items this way). RESOLVED.md is append-only and never purged,
        # so it must be included in the max() every single time.
        #
        # Scans both id formats (old "T-NNN", new "XOTNNN") to find the
        # true maximum regardless of which shape produced it, but always
        # ISSUES the new one -- T-164 never gets assigned even if for
        # some reason only "T-" entries existed; forward-only means
        # forward from here, not "whichever format was seen last."
        if RESOLVED.exists():
            ids += [int(t) for t in re.findall(
                r"^## \[.*?\] T-(\d+)\s", RESOLVED.read_text(), re.M)]
            ids += [int(t) for t in re.findall(
                r"^## \[.*?\] " + re.escape(NEW_PREFIX) + r"(\d+)\s",
                RESOLVED.read_text(), re.M)]
        return f"{NEW_PREFIX}{(max(ids) + 1) if ids else 1}"

    def check(self, r) -> None:
        """A1–A3 plus symbol validity. `r` needs .err(section, msg) and
        .warn(section, msg) — the release gate's Report qualifies."""
        for tid, (_, _, status, raw) in self.rows.items():
            if "✓" in raw:
                r.err("A1", f"closed item in register: {raw.strip()[:70]}")
        only_rows = set(self.rows) - set(self.items)
        only_items = set(self.items) - set(self.rows)
        if only_rows:
            r.err("A2", f"rows without detail blocks: {sorted(only_rows)}")
        if only_items:
            r.err("A2", f"detail blocks without rows: {sorted(only_items)}")
        for tid in set(self.rows) & set(self.items):
            it = self.items[tid]
            row = self.rows[tid][:3]
            det = (it.theme, it.priority, it.status)
            if row != det:
                r.err("A3", f"{tid}: table {row} vs detail {det}")
            if it.status not in STATUS_SYMBOLS:
                r.err("A3", f"{tid}: unknown status symbol {it.status!r}")


def load() -> Register:
    return Register(TRACKING.read_text())


def write_with_diff(path: Path, new_text: str, dry: bool, label: str) -> None:
    old = path.read_text() if path.exists() else ""
    if old == new_text:
        print(f"   no change: {path.name}")
        return
    if dry:
        diff = difflib.unified_diff(old.splitlines(keepends=True),
                                    new_text.splitlines(keepends=True),
                                    fromfile=f"a/{path.name}", tofile=f"b/{path.name}")
        sys.stdout.writelines(list(diff)[:80])
        print(f"   (dry-run) {label}: {path.name} not written")
        return
    path.write_text(new_text)
    print(f"   {label}: {path.name} updated")


def cmd_list(reg: Register) -> int:
    for tid in sorted(reg.items, key=_id_num):
        it = reg.items[tid]
        print(f"{tid}  {it.status}  {it.priority}  [{it.theme}]  {it.title}")
    return 0


def cmd_show(reg: Register, tid: str) -> int:
    if tid not in reg.items:
        print(f"no such item: {tid}", file=sys.stderr)
        return 1
    s, e = reg.spans[tid]
    print(reg.text[s:e].rstrip())
    return 0


def cmd_add(reg: Register, args) -> int:
    tid = args.id or reg.next_id()
    if tid in reg.rows or tid in reg.items:
        print(f"id already exists: {tid} (ids are never reused)", file=sys.stderr)
        return 1
    if args.status not in STATUS_SYMBOLS:
        print(f"invalid status {args.status!r}; use one of {STATUS_SYMBOLS}",
              file=sys.stderr)
        return 1
    body = args.body or ""
    if args.body_file:
        body = Path(args.body_file).read_text()
    if not body.strip():
        print("a register item needs a body (--body / --body-file): at "
              "minimum a Trigger line and a Scope line", file=sys.stderr)
        return 1

    text = reg.text
    # Row: insert after the last existing T-row in the status table.
    row_matches = list(re.finditer(r"^\| " + _ID_ALT + r" \|.*$", text, re.M))
    if not row_matches:
        print("cannot locate the status table", file=sys.stderr)
        return 1
    last = row_matches[-1]
    row = (f"| {tid} | {args.summary} | {args.theme} | {args.priority} "
           f"| {args.status} | {args.blocks or '—'} |")
    text = text[:last.end()] + "\n" + row + text[last.end():]

    # Detail: into the matching `## <theme>` group, else a new group at
    # the tail (before the trailing --- if present).
    section = (f"### {tid}. {args.summary}\n\n"
               f"Theme: {args.theme} · Priority: {args.priority} · "
               f"Status: {args.status}"
               + (f" · Blocks/after: {args.blocks}" if args.blocks else "")
               + "\n\n" + body.rstrip() + "\n\n")
    gm = re.search(rf"^## {re.escape(args.theme)}\s*$", text, re.M)
    if gm:
        tail = text[gm.end():]
        nm = re.search(r"^## |^---\s*$", tail, re.M)
        ins = gm.end() + (nm.start() if nm else len(tail))
        text = text[:ins] + section + text[ins:]
    else:
        tm = re.search(r"^---\s*$", text[::-1], re.M)
        if text.rstrip().endswith("---"):
            idx = text.rstrip().rfind("\n---")
            text = text[:idx] + f"\n## {args.theme}\n\n" + section + text[idx:]
        else:
            text = text.rstrip() + f"\n\n## {args.theme}\n\n" + section
    write_with_diff(TRACKING, text, args.dry_run, f"add {tid}")
    if not args.dry_run:
        print(f"filed {tid}; run `register.py check` — and remember the "
              "status table and field lines must not diverge")
    return 0


def cmd_close(reg: Register, args) -> int:
    tid = args.item
    if tid not in reg.items or tid not in reg.spans:
        print(f"no such item in the register: {tid}", file=sys.stderr)
        return 1
    if tid not in reg.rows:
        print(f"{tid} has a detail section but no table row — fix A2 first",
              file=sys.stderr)
        return 1
    it = reg.items[tid]
    date = args.date or datetime.date.today().isoformat()
    version = args.version

    # 1. Resolution entry: full detail text as at closure, stamped.
    entry = (f"## [{version}] {tid} — {it.title} (v{version}, {date})\n\n"
             f"Theme: {it.theme} · closed {version} · {date}\n"
             f"{it.body.rstrip()}\n\n"
             f"Cross-ref: CHANGELOG {version}.\n\n")
    resolved = RESOLVED.read_text()
    first = re.search(r"^## ", resolved, re.M)
    if not first:
        print("RESOLVED.md: cannot find insertion point (no '## ' entry)",
              file=sys.stderr)
        return 1
    new_resolved = resolved[:first.start()] + entry + resolved[first.start():]

    # 2. Register: remove detail section and row in one operation.
    s, e = reg.spans[tid]
    text = reg.text[:s] + reg.text[e:]
    raw_row = reg.rows[tid][3]
    text = text.replace(raw_row + "\n", "", 1)
    # Drop a theme group emptied by the removal.
    text = re.sub(rf"^## {re.escape(it.theme)}\s*\n+(?=(## |---\s*$))", "",
                  text, flags=re.M)

    write_with_diff(RESOLVED, new_resolved, args.dry_run, f"close {tid} (record)")
    write_with_diff(TRACKING, text, args.dry_run, f"close {tid} (register)")
    if not args.dry_run:
        print(f"closed {tid} at v{version}. Remaining by hand: the CHANGELOG "
              f"entry for {version} should cross-reference this closure "
              "(the changelog says what shipped; RESOLVED.md says what was "
              "wrong — they reference, never duplicate).")
    return 0


def cmd_check(reg: Register) -> int:
    class R:
        def __init__(self):
            self.errors = []

        def err(self, s, m):
            self.errors.append(f"[{s}] {m}")

        def warn(self, s, m):
            print(f"WARN [{s}] {m}")

    r = R()
    reg.check(r)
    for e in r.errors:
        print(f"ERROR {e}")
    if r.errors:
        print(f"REGISTER CHECK FAIL: {len(r.errors)} error(s)")
        return 1
    print(f"REGISTER CHECK OK: {len(reg.items)} open item(s)")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Live-register operations (docs/TRACKING.md)",
        epilog="See module docstring for the closure procedure this enforces.")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("list")
    p = sub.add_parser("show")
    p.add_argument("item")
    p = sub.add_parser("add")
    p.add_argument("--id", help="explicit id (default: next free)")
    p.add_argument("--summary", required=True)
    p.add_argument("--theme", required=True)
    p.add_argument("--priority", required=True, help="P1 (highest) .. P4")
    p.add_argument("--status", default="☐")
    p.add_argument("--blocks", default="")
    p.add_argument("--body")
    p.add_argument("--body-file")
    p.add_argument("--dry-run", action="store_true")
    p = sub.add_parser("close")
    p.add_argument("item")
    p.add_argument("--version", required=True)
    p.add_argument("--date")
    p.add_argument("--dry-run", action="store_true")
    sub.add_parser("check")
    args = ap.parse_args()

    reg = load()
    if args.cmd == "list":
        return cmd_list(reg)
    if args.cmd == "show":
        return cmd_show(reg, args.item)
    if args.cmd == "add":
        return cmd_add(reg, args)
    if args.cmd == "close":
        return cmd_close(reg, args)
    if args.cmd == "check":
        return cmd_check(reg)
    return 1


if __name__ == "__main__":
    sys.exit(main())
