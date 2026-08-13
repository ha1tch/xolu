#!/usr/bin/env python3
"""scripts/wave_progress.py — regenerates docs/SUBSTRATE_TRACKING.md's
own "## 1. Progress at a glance" summary from each wave's own per-item
status table below it.

Why this exists: that summary used to be hand-maintained prose with
hand-computed percentages -- exactly the kind of thing that drifts
from the data it's summarising, and did (Wave 1 was misreported as
1/2 for a stretch; see T-113 in RESOLVED.md). This script makes the
summary a pure function of the per-wave tables, which stay
hand-maintained (their per-item Notes are genuinely bespoke prose,
not something to generate) -- only the roll-up is mechanical now.

Per-wave percentage: a table row's Status column is one of the three
project-wide markers (✓ done, ◐ partial, ☐ not started). A row counts
as 1.0 item done, 0.5, or 0.0 respectively; percentage is that sum
over the row count. A "~" prefix marks any wave containing a ◐ row,
matching this project's own existing convention of flagging partial
waves as approximate rather than a precise fraction (Wave 2's own
prose already says this explicitly: "not a precise fraction of
anything countable" -- the formula agrees with that hand-written
figure, it doesn't override it).

Each wave also gets a "debt:" subtitle line, listing open register
items (docs/TRACKING.md) whose theme is mapped to that wave via
WAVE_THEMES -- open technical debt that shares a wave's subject
matter but was never one of that wave's own planned items. Items
already counted as SOME wave's own item (per that wave's own table
column 4, not a theme guess) are excluded even if their theme happens
to match a different wave -- T-81 is iolu-themed but is wave 8's own
item 37, so it must not double up as wave 6 debt. Waves absent from
WAVE_THEMES simply show no debt line, not an empty one.

A wave also gets a "blockers:" subtitle when any of its own items or
its debt is waiting on a currently-open prerequisite (an "After:
T-NN" field in TRACKING.md where T-NN is itself still open -- not
"After: none", not a reference to something already shipped, and not
an unfiled/unverifiable T-number like T-51, which T-52 names as its
own blocker but which has never actually been filed in TRACKING.md OR
RESOLVED.md). Grouped by blocker, not by blocked item, and blockers
can be cross-wave (T-81 is wave 8's own item but is blocked by T-70,
a wave 6 item -- that cross-wave signal is the point, not filtered).

Deliberately NOT preserved: hand-composed trailing annotations the
old summary carried per wave (e.g. "— all 6 scoped as T-76–T-81",
"shipped — T-127–T-129 in v0.21.3/v0.21.4..."). Those are narrative,
not data, and belong in each wave's own detail section below the
summary (untouched by this script) -- trying to mechanically preserve
hand-written trailing prose across regenerations is exactly the kind
of fragile pattern this script exists to avoid. The summary's job is
a fast glance; detail lives one section down, same as before.

Usage:
    python3 scripts/wave_progress.py           # regenerate in place
    python3 scripts/wave_progress.py --check   # exit 1 if it would change, don't write
    python3 scripts/wave_progress.py --show    # print to stdout, touch nothing (make waves)
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOC = ROOT / "docs" / "SUBSTRATE_TRACKING.md"

# Short display names for the summary line -- the wave headings below
# use fuller phrasing ("ID widening and system-scope reservation")
# than the terse summary form ("ID widening + sysmask") always has.
# Edited by hand when a wave is added; low cardinality, changes rarely.
SHORT_NAMES = {
    "12": "pkg/client blob support",
    "0": "Process foundation",
    "1": "ID widening + sysmask",
    "2": "Referential integrity",
    "3": "Chronicle substrate",
    "4": "bal",
    "5": "dxp",
    "6": "iolu operations",
    "7": "Events + system stores",
    "8": "OQL primitive queries",
    "9": "/loc",
    "9b": "/loc — bring to spec",
    "10": "/obj",
}

STATUS_WEIGHT = {"✓": 1.0, "◐": 0.5, "☐": 0.0}

HEADING_RE = re.compile(r"^### Wave (\S+) — [^(]+\(", re.M)
ROW_RE = re.compile(r"^\|\s*\d+\s*\|.*\|\s*([✓◐☐])\s*\|", re.M)


def parse_waves(text: str) -> list[dict]:
    headings = list(HEADING_RE.finditer(text))
    waves = []
    for i, m in enumerate(headings):
        wave_id = m.group(1)
        start = m.end()
        end = headings[i + 1].start() if i + 1 < len(headings) else len(text)
        section = text[start:end]
        statuses = ROW_RE.findall(section)
        if not statuses:
            print(f"WARNING: Wave {wave_id} heading found but no table rows parsed "
                  f"-- skipped, check the table format", file=sys.stderr)
            continue
        weights = [STATUS_WEIGHT[s] for s in statuses]
        done_equiv = sum(weights)
        total = len(weights)
        has_partial = "◐" in statuses
        waves.append({
            "id": wave_id, "done_equiv": done_equiv, "total": total,
            "has_partial": has_partial, "item_tnums": all_wave_tnums(section),
        })
    return waves


# Which register themes belong to which wave, for the debt-subtitle
# line below. Hand-curated (like SHORT_NAMES) rather than derived --
# a theme like "bal" mapping to wave 4 isn't mechanically inferable
# from anything in the data, it's a judgement call about which open
# debt genuinely belongs to a wave's own subject matter. Waves with no
# entry here (0, 1, 2, 9, 9b) currently have no open item whose theme
# clearly maps to them -- not an omission, checked against the actual
# theme list in TRACKING.md's status table each time this needed
# updating.
WAVE_THEMES = {
    "3": ["chronicle"],
    "4": ["bal"],
    "5": ["dxp"],
    "6": ["iolu"],
    "7": ["events", "system-stores"],
    "8": ["oql"],
    "10": ["obj"],
    "12": ["client"],
}

TRACKING = ROOT / "docs" / "TRACKING.md"
# Matches register.py's own NEW_PREFIX/_ID_ALT (2026-08-04, forward-only
# "XOT" prefix for new register items -- see register.py's own header
# comment for the full story). Kept as a separate local constant, not
# imported, since this is a standalone script like every other tool
# here; duplicated deliberately rather than adding a cross-script
# dependency for one shared regex fragment.
_ID_ALT = r"(?:T-|XOT)\d+"
ROW_T_RE = re.compile(r"^\|\s*(" + _ID_ALT + r")\s*\|[^|]*\|\s*([a-z0-9-]+)\s*\|", re.M)


# Found 2026-08-09 (v0.30.2 release): _ID_ALT above already matched
# both id formats correctly, but blockers_by_wave's own sort key
# (t.split("-")[1]) still assumed every id has a hyphen -- true for
# "T-136", false for "XOT176" (no hyphen at all), so .split("-")
# returns a single-element list and [1] raises IndexError the moment
# any XOT-prefixed id appears in a wave's own blocker set. This is
# register.py's own _id_num, duplicated here for the same reason
# _ID_ALT is duplicated rather than imported: this is a standalone
# script like every other tool in this directory.
def _id_num(tid: str) -> int:
    """The numeric portion of an id, old ("T-NNN") or new ("XOTNNN")
    format -- the two prefixes differ in length (2 chars vs. 3, and
    only one carries a hyphen), so a fixed-offset slice like the old
    t[2:] is wrong for one of them. Raises ValueError on anything
    matching neither shape, rather than silently returning a wrong
    number."""
    if tid.startswith("T-"):
        return int(tid[2:])
    if tid.startswith("XOT"):
        return int(tid[3:])
    raise ValueError(f"unrecognized id format: {tid!r}")


def debt_by_wave(full_text: str) -> dict[str, list[str]]:
    """Open register items (docs/TRACKING.md's own status table),
    grouped by wave via WAVE_THEMES, excluding anything that's already
    SOME wave's own item (see all_wave_tnums's own doc for why theme
    alone isn't a safe filter). Themes with no wave mapping are simply
    absent from the result -- there is no "unaffiliated" bucket here,
    that's the ~26 items this project already knows are pure backlog
    with no wave tie."""
    if not TRACKING.is_file():
        return {}
    text = TRACKING.read_text(encoding="utf-8")
    already_a_wave_item = all_wave_tnums(full_text)
    theme_to_wave = {}
    for wave_id, themes in WAVE_THEMES.items():
        for t in themes:
            theme_to_wave[t] = wave_id
    result: dict[str, list[str]] = {}
    for tnum, theme in ROW_T_RE.findall(text):
        if tnum in already_a_wave_item:
            continue
        wave_id = theme_to_wave.get(theme)
        if wave_id:
            result.setdefault(wave_id, []).append(tnum)
    return result


RANGE_RE = re.compile(r"T-(\d+)\s+through\s+T-(\d+)")
TNUM_RE = re.compile(r"T-(\d+)")
# Column 4 of a wave's own item table: "Closed" (waves 0-5, version or
# T-number) or "Register item" (waves 6-10, T-number). Scoped to table
# ROWS specifically (line starts with "| <digits> |"), not prose --
# a file-wide T-number regex incorrectly swept up T-68 from its own
# explanatory footnote sentence ("T-68 is a related but distinct
# gap... not item 24 itself"), which explicitly says it is NOT one of
# wave 6's items. Only the table's own column 4 is authoritative for
# "this T-number IS one of this wave's numbered items."
ITEM_COL4_RE = re.compile(r"^\|\s*\d+\s*\|[^|]*\|\s*[✓◐☐]\s*\|([^|]*)\|", re.M)


def all_wave_tnums(text: str) -> set[str]:
    """Every T-number that is literally one of SOME wave's own
    numbered items, per that wave's own table column 4 -- not just
    literal T-NN mentions (which also silently drop the middle of
    "T-07 through T-13" range notation) but the expanded range too.
    Used as a global exclusion set for the debt calculation: a theme
    match alone isn't enough (T-81 is iolu-themed but is wave 8's own
    item 37, not wave 6 debt -- it must be excluded from wave 6's
    debt list precisely because it's already counted, in a different
    wave, not because of its theme)."""
    result = set()
    for col4 in ITEM_COL4_RE.findall(text):
        for lo, hi in RANGE_RE.findall(col4):
            for n in range(int(lo), int(hi) + 1):
                result.add(f"T-{n:02d}" if n < 100 else f"T-{n}")
        for n in TNUM_RE.findall(col4):
            result.add(f"T-{n}")
    return result


def bar(pct: float) -> str:
    filled = round(pct / 5)
    return "█" * filled + "░" * (20 - filled)


def fmt_item_count(done_equiv: float, total: int) -> str:
    d = f"{done_equiv:g}"
    return f"{d}/{total} items"


AFTER_RE = re.compile(r"After:\s*(" + _ID_ALT + r")")
TRACKING_ROW_RE = re.compile(
    r"^\| (" + _ID_ALT + r") \| ([^|]*) \| ([a-z0-9-]+) \| (P\d) \| ([✓◐☐]) \| ([^|]*) \|", re.M)


def blockers_by_wave(full_text: str, waves: list[dict]) -> dict[str, list[tuple[str, list[str]]]]:
    """For each wave's own items plus its debt items, checks their
    "After: T-NN" field (docs/TRACKING.md) and keeps only references
    that resolve to a CURRENTLY OPEN item -- not "After: none", not a
    version/item-number reference, and not a T-number that turns out
    to be unfiled or already closed (T-52 says "After: T-51", but
    T-51 isn't filed in TRACKING.md OR RESOLVED.md at all -- an
    unverifiable reference isn't a real blocker, it's noise). Returns
    wave_id -> [(blocker_tnum, [items it blocks in this wave]), ...],
    grouped by blocker so "T-76 blocks four things here" reads as one
    line, not four repeated ones. A blocker can be outside the wave
    entirely (T-81, wave 8's own item, is "After: T-70" -- a wave 6
    item) -- that cross-wave case is exactly the useful signal, not
    filtered out.
    """
    if not TRACKING.is_file():
        return {}
    tracking_text = TRACKING.read_text(encoding="utf-8")
    rows = TRACKING_ROW_RE.findall(tracking_text)
    open_tnums = {tnum for tnum, *_ in rows}
    after_field = {}
    for tnum, _s, _th, _p, _st, blocks in rows:
        m = AFTER_RE.search(blocks)
        if m and m.group(1) in open_tnums:
            after_field[tnum] = m.group(1)

    debt = debt_by_wave(full_text)
    result: dict[str, list[tuple[str, list[str]]]] = {}
    for w in waves:
        wave_scope = set(w["item_tnums"]) | set(debt.get(w["id"], []))
        by_blocker: dict[str, list[str]] = {}
        for item in sorted(wave_scope, key=_id_num):
            blocker = after_field.get(item)
            if blocker:
                by_blocker.setdefault(blocker, []).append(item)
        if by_blocker:
            result[w["id"]] = sorted(by_blocker.items())
    return result


def render_table(waves: list[dict], full_text: str) -> str:
    lines = []
    debt = debt_by_wave(full_text)
    blockers = blockers_by_wave(full_text, waves)
    label_w = max(len(f"Wave {w['id']}") for w in waves) + 2
    for w in waves:
        pct = 100 * w["done_equiv"] / w["total"] if w["total"] else 0
        pct_s = f"~{round(pct)}%" if w["has_partial"] else f"{round(pct)}%"
        label = f"Wave {w['id']}".ljust(label_w)
        name = SHORT_NAMES.get(w["id"], f"(unnamed wave {w['id']})")
        lines.append(
            f"{label}{name:<26}  {bar(pct)}  {pct_s:>5}  "
            f"({fmt_item_count(w['done_equiv'], w['total'])})"
        )
        indent = " " * (label_w + 26 + 2)
        wave_debt = debt.get(w["id"])
        if wave_debt:
            lines.append(f"{indent}debt: {', '.join(wave_debt)}")
        wave_blockers = blockers.get(w["id"])
        if wave_blockers:
            parts = [f"{blocker} blocks {', '.join(blocked)}"
                     for blocker, blocked in wave_blockers]
            lines.append(f"{indent}blockers: {'; '.join(parts)}")
    return "\n".join(lines)


def render_overall(waves: list[dict]) -> str:
    total_done = sum(w["done_equiv"] for w in waves)
    total_items = sum(w["total"] for w in waves)
    pct = round(100 * total_done / total_items) if total_items else 0
    done_s = f"{total_done:g}"
    return f"Overall by item count: {done_s} of {total_items} items \u2248 **{pct}%**"


def main() -> int:
    check = "--check" in sys.argv
    show = "--show" in sys.argv
    text = DOC.read_text(encoding="utf-8")
    waves = parse_waves(text)
    if not waves:
        print("no waves parsed -- aborting, not touching the file", file=sys.stderr)
        return 1

    new_table = render_table(waves, text)
    new_overall = render_overall(waves)

    if show:
        print(new_table)
        print()
        print(new_overall)
        return 0

    table_re = re.compile(r"(## 1\. Progress at a glance\n.*?```\n).*?(\n```\n)", re.S)
    m = table_re.search(text)
    if not m:
        print("could not find the '## 1. Progress at a glance' fenced block", file=sys.stderr)
        return 1
    new_text = text[:m.start()] + m.group(1) + new_table + m.group(2) + text[m.end():]

    overall_re = re.compile(r"Overall by item count: [\d.]+ of \d+ items \u2248 \*\*\d+%\*\*")
    if not overall_re.search(new_text):
        print("could not find the 'Overall by item count' line", file=sys.stderr)
        return 1
    new_text = overall_re.sub(new_overall, new_text)

    if new_text == text:
        print("wave_progress: already up to date")
        return 0

    if check:
        print("wave_progress: SUBSTRATE_TRACKING.md is stale -- run without --check to regenerate")
        return 1

    DOC.write_text(new_text, encoding="utf-8")
    print(f"wave_progress: regenerated ({len(waves)} waves)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
