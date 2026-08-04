#!/usr/bin/env python3
"""scripts/add_wave.py -- adds a new wave to the substrate programme
deterministically: docs/SUBSTRATE_TRACKING.md §2 (the per-item table
wave_progress.py reads) and docs/SUBSTRATE_DEVELOPMENT_PLAN.md (the
short pointer paragraph), with the wave number and item numbers
computed from the actual current state of both documents -- not
supplied by the caller and not guessed.

Why this exists: adding wave 12 by hand for pkg/client blob support
nearly collided with an EXISTING soft reservation -- SUBSTRATE_
TRACKING.md's own prose already said "if scheduled later, plausibly
wave 11" for an unrelated item (/far, /dxp/mxn), a reservation that
lives only in prose, not in a formal "### Wave 11" heading a naive
"highest heading + 1" scan would have missed entirely. This script
scans for BOTH: formal `### Wave N` headings and any other "wave N"
mention anywhere in either document, so a soft reservation like that
one is caught and refused rather than silently overwritten.

Item numbers are global and sequential across the whole programme,
never reused once assigned (docs/TRACKING_PRACTICES.md §2) -- this
script continues from the highest `| NN |` row it finds in any
existing wave table, the same number space wave 9b's own items
(51-56) continued from wave 10's (45-50) despite sitting between
waves 9 and 10 in the roadmap.

What this script does NOT do: write the load-bearing prose. The
SUBSTRATE_DEVELOPMENT_PLAN.md paragraph explaining why a wave exists,
what it depends on, and how it's sequenced is exactly the kind of
judgement call this project's own conventions treat as something a
person (or Claude, thinking about it) writes deliberately each time --
this script places it correctly and consistently, it doesn't compose
it. Pass that text in with --plan-note.

Usage:
    python3 scripts/add_wave.py \\
        --name "pkg/client blob support" \\
        --ideal-days 2 \\
        --items-json '[{"summary": "...", "register_item": "T-142"}]' \\
        --plan-note "Reopens the client's own documented v0.16.0 exclusion..." \\
        [--wave-number 12]   # override the computed number; still checked for collision
        [--dry-run]          # print what would change, touch nothing
"""
import argparse
import json
import re
import subprocess
import sys
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TRACKING = ROOT / "docs" / "SUBSTRATE_TRACKING.md"
PLAN = ROOT / "docs" / "SUBSTRATE_DEVELOPMENT_PLAN.md"

WAVE_HEADING_RE = re.compile(r"^### Wave (\S+) —", re.M)
WAVE_MENTION_RE = re.compile(r"\bwave (\d+)\b", re.I)
ITEM_ROW_RE = re.compile(r"^\|\s*(\d+)\s*\|", re.M)


def existing_wave_numbers(text: str) -> set[int]:
    """Every integer wave number already in use OR reserved -- formal
    headings AND prose mentions like "plausibly wave 11". Alphabetic
    sub-waves (9b) are excluded from this integer set deliberately;
    they share wave 9's own number by design, not a new one."""
    nums = set()
    for m in WAVE_HEADING_RE.finditer(text):
        digits = re.match(r"\d+", m.group(1))
        if digits:
            nums.add(int(digits.group()))
    for m in WAVE_MENTION_RE.finditer(text):
        nums.add(int(m.group(1)))
    return nums


def next_wave_number(requested: int | None) -> int:
    tracking_text = TRACKING.read_text(encoding="utf-8")
    plan_text = PLAN.read_text(encoding="utf-8")
    taken = existing_wave_numbers(tracking_text) | existing_wave_numbers(plan_text)

    if requested is not None:
        if requested in taken:
            raise SystemExit(
                f"--wave-number {requested} collides with an existing heading or "
                f"prose reservation in SUBSTRATE_TRACKING.md/SUBSTRATE_DEVELOPMENT_PLAN.md. "
                f"Taken/reserved: {sorted(taken)}"
            )
        return requested

    candidate = max(taken, default=0) + 1
    while candidate in taken:
        candidate += 1
    return candidate


def next_item_number(n_items: int) -> int:
    text = TRACKING.read_text(encoding="utf-8")
    existing = [int(n) for n in ITEM_ROW_RE.findall(text)]
    return (max(existing, default=0) + 1) if existing else 1


def build_tracking_section(wave_num: int, name: str, ideal_days: float,
                            items: list[dict], start_item: int) -> str:
    today = date.today().isoformat()
    lines = [
        f"### Wave {wave_num} — {name} ({len(items)} item{'s' if len(items) != 1 else ''}, "
        f"ideal {ideal_days}d, added {today})",
        "",
        "| # | Summary | Status | Register item |",
        "|---|---|---|---|",
    ]
    for i, item in enumerate(items):
        item_num = start_item + i
        reg = item.get("register_item", "not yet filed")
        lines.append(f"| {item_num} | {item['summary']} | ☐ | {reg} |")
    lines.append("")
    lines.append(f"**Wave {wave_num}: 0/{len(items)}, not started.**")
    lines.append("")
    return "\n".join(lines)


def build_plan_paragraph(wave_num: int, name: str, ideal_days: float, plan_note: str) -> str:
    today = date.today().isoformat()
    return (f"**Wave {wave_num} — {name} (≈ {ideal_days}d, added {today}).** "
            f"{plan_note}\n")


def insert_tracking_section(section: str) -> None:
    text = TRACKING.read_text(encoding="utf-8")
    m = re.search(r"\n## 3\.", text)
    if not m:
        raise SystemExit("could not find '## 3.' in SUBSTRATE_TRACKING.md to insert before")
    insertion_point = m.start() + 1
    new_text = text[:insertion_point] + section + "\n---\n\n" + text[insertion_point:]
    TRACKING.write_text(new_text, encoding="utf-8")


def insert_plan_paragraph(paragraph: str) -> None:
    text = PLAN.read_text(encoding="utf-8")
    headings = list(WAVE_HEADING_RE.finditer(text)) or list(
        re.finditer(r"^\*\*Wave \S+ —", text, re.M))
    if headings:
        last = headings[-1]
        para_end = text.find("\n\n", last.end())
        insertion_point = (para_end + 2) if para_end != -1 else len(text)
    else:
        insertion_point = len(text)
    new_text = text[:insertion_point] + "\n" + paragraph + "\n" + text[insertion_point:]
    PLAN.write_text(new_text, encoding="utf-8")


def insert_short_name(wave_num: int, name: str) -> None:
    """Adds this wave's entry to wave_progress.py's own SHORT_NAMES
    dict, right before the closing brace. Without this, wave_progress.py
    renders "(unnamed wave N)" -- a real gap found by running this
    tool end to end the first time, not a hypothetical: SHORT_NAMES
    is hand-curated by design (a wave's short display form isn't
    mechanically derivable from its full name), but leaving that as a
    separate manual follow-up step undermines the determinism this
    tool exists for. Guarded: refuses if the key already exists rather
    than silently duplicating or overwriting.
    """
    wp_path = ROOT / "scripts" / "wave_progress.py"
    text = wp_path.read_text(encoding="utf-8")
    key = f'"{wave_num}"'
    if re.search(rf'^\s*{re.escape(key)}\s*:', text, re.M):
        print(f"wave_progress.py SHORT_NAMES already has an entry for {key} -- not touching it",
              file=sys.stderr)
        return
    m = re.search(r"(SHORT_NAMES = \{\n)", text)
    if not m:
        raise SystemExit("could not find 'SHORT_NAMES = {' in wave_progress.py")
    escaped_name = name.replace('"', '\\"')
    new_line = f'    "{wave_num}": "{escaped_name}",\n'
    new_text = text[:m.end()] + new_line + text[m.end():]
    wp_path.write_text(new_text, encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                  formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--name", required=True)
    ap.add_argument("--ideal-days", type=float, required=True)
    ap.add_argument("--items-json", required=True,
                     help='JSON list: [{"summary": "...", "register_item": "T-NNN"}]')
    ap.add_argument("--plan-note", required=True,
                     help="Rationale paragraph for SUBSTRATE_DEVELOPMENT_PLAN.md -- written "
                          "deliberately, not generated.")
    ap.add_argument("--wave-number", type=int, default=None,
                     help="Override the computed wave number. Still checked for collision.")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    try:
        items = json.loads(args.items_json)
    except json.JSONDecodeError as e:
        raise SystemExit(f"--items-json is not valid JSON: {e}")
    if not items or not isinstance(items, list):
        raise SystemExit("--items-json must be a non-empty JSON list")
    for item in items:
        if "summary" not in item:
            raise SystemExit(f"item missing 'summary': {item}")

    wave_num = next_wave_number(args.wave_number)
    start_item = next_item_number(len(items))
    end_item = start_item + len(items) - 1

    tracking_section = build_tracking_section(
        wave_num, args.name, args.ideal_days, items, start_item)
    plan_paragraph = build_plan_paragraph(
        wave_num, args.name, args.ideal_days, args.plan_note)

    print(f"Wave number: {wave_num}  (computed; checked against headings + prose reservations)")
    print(f"Item numbers: {start_item}"
          + (f"-{end_item}" if end_item != start_item else "") + "\n")
    print("--- SUBSTRATE_TRACKING.md section ---")
    print(tracking_section)
    print("--- SUBSTRATE_DEVELOPMENT_PLAN.md paragraph ---")
    print(plan_paragraph)

    if args.dry_run:
        print("(--dry-run: nothing written)")
        return 0

    insert_tracking_section(tracking_section)
    insert_plan_paragraph(plan_paragraph)
    insert_short_name(wave_num, args.name)

    rc = subprocess.run([sys.executable, str(ROOT / "scripts" / "wave_progress.py")]).returncode
    if rc != 0:
        print("wave_progress.py regeneration failed -- check SUBSTRATE_TRACKING.md's "
              "own fenced block by hand", file=sys.stderr)
        return rc

    print(f"\nadd_wave: Wave {wave_num} added, items {start_item}-{end_item}, "
          f"bars regenerated.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
