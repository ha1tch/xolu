#!/usr/bin/env python3
"""
scripts/charts.py — xolu test-suite visualiser.

Terminal mode (default, no --output):
  Reads pkg|tests|cover lines from stdin and renders two charts:
    heatmap  — coverage heat map, one bar per package
    treemap  — squarified test-count treemap
  Requires a colour-capable terminal.  Truecolor (24-bit) is used when the
  terminal supports it (COLORTERM=truecolor/24bit, or a known-capable $TERM);
  256-colour mode is used as a fallback.

File export mode (--output FILE):
  Generates a static two-panel chart using matplotlib.
  Supported extensions: .svg  .png  .pdf
  matplotlib must be installed: pip install matplotlib

Usage
-----
  # Via run_tests.sh (preferred):
  ./run_tests.sh --charts

  # Direct — terminal:
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py heatmap
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py treemap
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py both

  # Direct — file export:
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py both --output charts.svg
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py both --output charts.png
  echo "pkg/graph|266|72.9" | python3 scripts/charts.py both --output report.pdf

  # From go test output:
  go test -v -coverprofile=cover.out ./pkg/... 2>&1 \\
    | python3 scripts/charts.py both --from-go-output --output charts.svg
"""

import sys
import os
import re
import struct
import fcntl
import termios
import argparse
from collections import defaultdict
from pathlib import Path

# ---------------------------------------------------------------------------
# 20-step discrete palette  (5% per step, 0–95 as floor values)
#
# Spectrum split:
#   0–79%  (16 steps = 80%) — purple / red / orange / amber / yellow / olive / transition
#   80–100% (4 steps = 20%) — vivid green / green-teal / teal / blue-teal
#
# This compression pushes most of the visual range into the "bad" zone,
# making greens genuinely distinctive and easy to read at a glance.
#
# Hand-tuned so that:
#  - Each step is visually distinct from its neighbours in both hue and
#    luminance, including in 256-colour terminal fallback mode.
#  - Steps 0–10% are purple/magenta — legitimately very bad coverage.
#  - Step 55% is a lighter yellow, brighter than the 50% yellow, providing
#    a clear peak before the gradient descends into olive at 60%.
#  - The 65% bucket is the first green — the visual "you're ok" threshold.
# ---------------------------------------------------------------------------

_PALETTE = {
     0: (0.52, 0.00, 0.52),   # rotten purple      — 0–4%
     5: (0.55, 0.02, 0.48),   # dark purple-magenta — 5–9%
    10: (0.62, 0.04, 0.30),   # purple-red          — 10–14%
    15: (0.75, 0.06, 0.06),   # red                 — 15–19%
    20: (0.83, 0.10, 0.04),   # bright red          — 20–24%
    25: (0.85, 0.30, 0.00),   # orange-red          — 25–29%
    30: (0.86, 0.40, 0.00),   # dark orange         — 30–34%
    35: (0.85, 0.50, 0.00),   # orange              — 35–39%
    40: (0.82, 0.60, 0.00),   # amber               — 40–44%
    45: (0.78, 0.68, 0.00),   # golden              — 45–49%
    50: (0.70, 0.72, 0.00),   # yellow              — 50–54%  ← anchor
    55: (0.80, 0.92, 0.04),   # warm lemon          — 55–59%
    60: (0.94, 0.96, 0.08),   # lemon               — 60–64%
    65: (0.92, 0.94, 0.16),   # bright lemon        — 65–69%  ← midpoint 50→85
    70: (0.62, 0.88, 0.04),   # lemon-lime          — 70–74%
    75: (0.40, 0.84, 0.04),   # warm lime           — 75–79%
    80: (0.20, 0.78, 0.04),   # yellow-green        — 80–84%
    85: (0.02, 0.68, 0.20),   # vivid green         — 85–89%  ← anchor
    90: (0.00, 0.74, 0.46),   # teal-green          — 90–94%
    95: (0.00, 0.78, 0.68),   # blue-teal           — 95–100%
}

_GREY_UNKNOWN = (0.42, 0.42, 0.42)


def cover_rgb(pct):
    """Return (R, G, B) in 0.0–1.0 for a coverage percentage (or None)."""
    if pct is None:
        return _GREY_UNKNOWN
    bucket = min(95, (int(pct) // 5) * 5)
    return _PALETTE[bucket]


def luminance(r, g, b):
    return 0.299 * r + 0.587 * g + 0.114 * b


def text_colour(bg_rgb):
    """Return a legible foreground colour for bg_rgb."""
    lum = luminance(*bg_rgb)
    return (0.08, 0.08, 0.08) if lum >= 0.40 else (0.94, 0.94, 0.94)


# ---------------------------------------------------------------------------
# Input parsing
# ---------------------------------------------------------------------------

def parse_pipe(stream):
    rows = []
    for line in stream:
        line = line.strip()
        if not line:
            continue
        parts = line.split('|')
        if len(parts) != 3:
            continue
        pkg, tests, cover = parts
        try:
            tests = int(tests)
        except ValueError:
            tests = 0
        try:
            cover = float(cover)
        except ValueError:
            cover = None
        rows.append({'pkg': pkg, 'tests': tests, 'cover': cover})
    return rows


def parse_go_output(stream):
    pkg = None
    counts = defaultdict(int)
    covers = {}
    for line in stream:
        line = line.rstrip()
        m = re.match(r'^ok\s+([\w./\-]+)\s+[\d.]+s\s+coverage:\s*([\d.]+)%', line)
        if m:
            p = m.group(1).replace('github.com/ha1tch/xolu/', '')
            covers[p] = float(m.group(2))
            pkg = p
            continue
        m = re.match(r'^ok\s+([\w./\-]+)\s+[\d.]+s', line)
        if m:
            p = m.group(1).replace('github.com/ha1tch/xolu/', '')
            covers.setdefault(p, None)
            pkg = p
            continue
        if re.match(r'^\s*--- (?:PASS|FAIL|SKIP): .+ \(', line) and pkg:
            counts[pkg] += 1
    return [{'pkg': p, 'tests': counts[p], 'cover': covers.get(p)}
            for p in sorted(counts)]


# ---------------------------------------------------------------------------
# Terminal colour support
# ---------------------------------------------------------------------------

RESET = '\x1b[0m'
BOLD  = '\x1b[1m'
DIM   = '\x1b[2m'


def _detect_colour():
    """
    Return 'truecolor', '256', or 'none'.
    Truecolor: COLORTERM=truecolor|24bit, or known-capable $TERM.
    256-colour: any of the usual xterm-256color suspects.
    None: dumb terminal or no colour support.
    """
    colorterm = os.environ.get('COLORTERM', '').lower()
    if colorterm in ('truecolor', '24bit'):
        return 'truecolor'
    term = os.environ.get('TERM', '')
    term_prog = os.environ.get('TERM_PROGRAM', '')
    truecolor_terms = ('kitty', 'wezterm', 'alacritty', 'foot', 'vte',
                       'iterm.app', 'hyper')
    if any(t in term.lower() or t in term_prog.lower() for t in truecolor_terms):
        return 'truecolor'
    if '256color' in term or '256color' in term_prog:
        return '256'
    if term in ('xterm', 'rxvt-unicode', 'screen', 'tmux'):
        return '256'
    return 'none'


_COLOUR_MODE = _detect_colour()


def _to_256(r, g, b):
    def c6(v): return max(0, min(5, round(v * 5)))
    return 16 + 36 * c6(r) + 6 * c6(g) + c6(b)


def ansi_bg(rgb):
    r, g, b = rgb
    if _COLOUR_MODE == 'truecolor':
        return f'\x1b[48;2;{int(r*255)};{int(g*255)};{int(b*255)}m'
    return f'\x1b[48;5;{_to_256(r,g,b)}m'


def ansi_fg(rgb):
    r, g, b = rgb
    if _COLOUR_MODE == 'truecolor':
        return f'\x1b[38;2;{int(r*255)};{int(g*255)};{int(b*255)}m'
    return f'\x1b[38;5;{_to_256(r,g,b)}m'


def term_size():
    try:
        h, w = struct.unpack('hh', fcntl.ioctl(1, termios.TIOCGWINSZ, b'\0' * 4))
        return w, h
    except Exception:
        return int(os.environ.get('COLUMNS', 100)), int(os.environ.get('LINES', 40))


# ---------------------------------------------------------------------------
# Terminal heatmap
# ---------------------------------------------------------------------------

def terminal_heatmap(rows, width):
    shorts   = [r['pkg'].replace('github.com/ha1tch/xolu/', '') for r in rows]
    maxname  = max((len(s) for s in shorts), default=10)
    bar_w    = max(10, width - maxname - 14)

    print(f'\n  {BOLD}Coverage heat map{RESET}\n')
    col_mid = bar_w // 2
    col_75  = int(bar_w * 0.75)
    header_bar = ' ' * col_mid + '50%' + ' ' * (col_75 - col_mid - 3) + '75%'
    print(f'  {"Package":<{maxname}}  0%{header_bar:{bar_w - 2}}100%  {"Cover":>6}')
    print(f'  {"-" * maxname}  {"-" * bar_w}  {"-----":>6}')

    for short, row in zip(shorts, rows):
        pct   = row['cover']
        bg    = cover_rgb(pct)
        fg    = text_colour(bg)
        empty = cover_rgb(0)

        if pct is None:
            filled  = 0
            pct_str = '  n/a '
        else:
            filled  = max(0, min(bar_w, round(bar_w * pct / 100.0)))
            pct_str = f'{pct:5.1f}%'

        bar = (
            f'{ansi_bg(bg)}{ansi_fg(fg)}{" " * filled}{RESET}'
            f'{ansi_bg(empty)}{ansi_fg(text_colour(empty))}{" " * (bar_w - filled)}{RESET}'
        )
        print(f'  {short:<{maxname}}  {bar}  {pct_str}')

    # Legend — one swatch per palette step
    print()
    print(f'  {BOLD}Legend:{RESET} ', end='')
    for step in sorted(_PALETTE):
        bg = _PALETTE[step]
        fg = text_colour(bg)
        print(f'{ansi_bg(bg)}{ansi_fg(fg)}{step:3d}%{RESET}', end='')
    print()


# ---------------------------------------------------------------------------
# Terminal treemap
# ---------------------------------------------------------------------------

def _squarify(sizes, x, y, w, h):
    if not sizes:
        return []
    if len(sizes) == 1:
        return [(x, y, w, h)]

    def worst(row, area, length):
        if not row or area == 0 or length == 0:
            return float('inf')
        s, r = max(row), min(row)
        l2 = length * length
        return max(l2 * s / area ** 2, area ** 2 / (l2 * r)) if r else float('inf')

    results, rem = [], list(sizes)
    cx, cy, cw, ch = x, y, w, h

    while rem:
        horiz  = cw >= ch
        length = ch if horiz else cw
        row, row_area, best = [], 0, float('inf')
        for s in rem:
            row.append(s)
            row_area += s
            ratio = worst(row, row_area, length)
            if ratio > best:
                row.pop(); row_area -= s; break
            best = ratio
        if not row:
            row = [rem[0]]; row_area = rem[0]

        if horiz:
            strip = row_area / ch if ch else 0
            ty = cy
            for s in row:
                rh = s / row_area * ch if row_area else 0
                results.append((cx, ty, strip, rh)); ty += rh
            cx += strip; cw -= strip
        else:
            strip = row_area / cw if cw else 0
            tx = cx
            for s in row:
                rw = s / row_area * cw if row_area else 0
                results.append((tx, cy, rw, strip)); tx += rw
            cy += strip; ch -= strip

        for s in row:
            rem.remove(s)

    return results


def terminal_treemap(rows, width):
    MAP_H = 22
    MAP_W = width - 4
    # Lay out in "half-column" space (2 cols = 1 unit wide = 1 unit tall)
    cw = MAP_W * 2.0
    ch = float(MAP_H)

    data = sorted([r for r in rows if r['tests'] > 0],
                  key=lambda r: r['tests'], reverse=True)
    if not data:
        print('  (no data)'); return

    total = sum(r['tests'] for r in data)
    sizes = [r['tests'] / total * cw * ch for r in data]
    rects = _squarify(sizes, 0, 0, cw, ch)

    EMPTY = (' ', None, None)
    grid  = [[EMPTY] * MAP_W for _ in range(MAP_H)]

    for (rx, ry, rw, rh), row in zip(rects, data):
        col_x = int(round(rx / 2))
        col_w = max(1, int(round(rw / 2)))
        row_y = int(round(ry))
        row_h = max(1, int(round(rh)))
        bg = cover_rgb(row['cover'])
        fg = text_colour(bg)

        for r in range(row_y, min(row_y + row_h, MAP_H)):
            for c in range(col_x, min(col_x + col_w, MAP_W)):
                border = (r == row_y or r == row_y + row_h - 1
                          or c == col_x or c == col_x + col_w - 1)
                glyph = '·' if (border and col_w > 2 and row_h > 2) else ' '
                grid[r][c] = (glyph, bg, fg)

        inner_w = col_w - 2
        inner_h = row_h - 2
        if inner_w >= 3 and inner_h >= 1:
            short = row['pkg'].split('/')[-1]
            lines = [short[:inner_w]]
            if inner_h >= 2:
                lines.append(f'{row["tests"]}t'[:inner_w])
            if inner_h >= 3 and row['cover'] is not None:
                lines.append(f'{row["cover"]:.0f}%'[:inner_w])
            for i, text in enumerate(lines):
                r = row_y + 1 + i
                if r >= row_y + row_h - 1 or r >= MAP_H: break
                pad = max(0, (inner_w - len(text)) // 2)
                for j, ch_c in enumerate(text):
                    ci = col_x + 1 + pad + j
                    if ci >= col_x + col_w - 1 or ci >= MAP_W: break
                    ex = grid[r][ci]
                    grid[r][ci] = (ch_c, ex[1], ex[2])

    print(f'\n  {BOLD}Test count treemap  {DIM}(tile area ∝ test count){RESET}\n')
    for row in grid:
        sys.stdout.write('  ')
        for (ch_c, bg, fg) in row:
            if bg is not None:
                sys.stdout.write(f'{ansi_bg(bg)}{ansi_fg(fg)}{ch_c}{RESET}')
            else:
                sys.stdout.write(ch_c)
        sys.stdout.write('\n')

    print()
    col = 0
    for (_, _, _, _), row in zip(rects, data):
        short   = row['pkg'].split('/')[-1]
        bg      = cover_rgb(row['cover'])
        fg      = text_colour(bg)
        pct_str = f'{row["cover"]:.0f}%' if row['cover'] is not None else 'n/a'
        swatch  = f'{ansi_bg(bg)}{ansi_fg(fg)} {short} {RESET}'
        label   = f'{swatch}{DIM} {row["tests"]}t {pct_str}{RESET}'
        if col == 0:
            sys.stdout.write('  ')
        sys.stdout.write(f'{label:<42}')
        col += 1
        if col >= 3:
            print(); col = 0
    if col > 0:
        print()


# ---------------------------------------------------------------------------
# matplotlib export (SVG / PNG / PDF)
# ---------------------------------------------------------------------------

def export_charts(rows, output_path):
    try:
        import matplotlib
        matplotlib.use('Agg')
        import matplotlib.pyplot as plt
        import matplotlib.patches as patches
        from matplotlib.colors import LinearSegmentedColormap
    except ImportError:
        print('ERROR: matplotlib is required for file export.\n'
              '  pip install matplotlib', file=sys.stderr)
        sys.exit(1)

    shorts = [r['pkg'].replace('github.com/ha1tch/xolu/', '') for r in rows]
    n      = len(rows)

    # Build a smooth matplotlib colormap from the same discrete palette so
    # the colourbar legend and the bars use identical colours.
    steps   = sorted(_PALETTE) + [100]
    colours = [_PALETTE[min(95, s)] for s in steps]
    norm_stops = [s / 100.0 for s in steps]
    cmap = LinearSegmentedColormap.from_list(
        'xolu_cov',
        list(zip(norm_stops, colours)),
        N=256
    )
    import matplotlib.cm as cm
    import matplotlib.colors as mcolors
    norm = mcolors.Normalize(vmin=0, vmax=100)

    BG_DARK  = '#0f0f1a'
    BG_PANEL = '#16213e'

    fig_h = max(6, 0.38 * n + 4)
    fig   = plt.figure(figsize=(16, fig_h + 9), constrained_layout=False)
    fig.patch.set_facecolor(BG_DARK)

    gs = fig.add_gridspec(2, 1,
                          height_ratios=[fig_h, 9],
                          hspace=0.10,
                          left=0.02, right=0.98,
                          top=0.97, bottom=0.02)

    # ── Panel 1: heatmap ────────────────────────────────────────────────
    ax1 = fig.add_subplot(gs[0])
    ax1.set_facecolor(BG_PANEL)
    ax1.set_xlim(0, 1)
    ax1.set_ylim(-0.5, n - 0.5)
    ax1.set_yticks(range(n))
    ax1.set_yticklabels(shorts[::-1], fontsize=8,
                        color='#d8d8d8', fontfamily='monospace')
    ax1.set_xticks([0, 0.25, 0.50, 0.65, 0.75, 0.85, 1.0])
    ax1.set_xticklabels(['0%', '25%', '50%', '65%', '75%', '85%', '100%'],
                         fontsize=7.5, color='#888')
    ax1.tick_params(axis='both', which='both', length=0, pad=4)
    for spine in ax1.spines.values():
        spine.set_visible(False)
    ax1.set_title('Coverage heat map', color='#e0e0e0',
                  fontsize=11, loc='left', pad=8, fontweight='bold')

    # Threshold guide lines
    for x, label in ((0.65, '65%'), (0.75, '75%'), (0.85, '85%')):
        ax1.axvline(x=x, color='#ffffff28', linewidth=0.7,
                    linestyle='--', zorder=0)

    for i, row in enumerate(reversed(rows)):
        pct  = row['cover'] or 0
        rgba = cmap(norm(pct))
        bg   = rgba[:3]
        fg   = text_colour(bg)
        val  = pct / 100.0

        # Unfilled background (dark red bucket)
        ax1.barh(i, 1.0, left=0, height=0.76,
                 color=_PALETTE[0], alpha=0.35, zorder=1)
        # Filled bar
        ax1.barh(i, val, left=0, height=0.76,
                 color=bg, alpha=0.97, zorder=2)
        # Label
        pct_str = (f'{row["cover"]:.1f}%' if row['cover'] is not None
                   else 'n/a')
        lx = max(val - 0.005, 0.005)
        ax1.text(lx, i, pct_str, va='center', ha='right',
                 fontsize=7.5, color=fg, fontfamily='monospace',
                 fontweight='bold', zorder=3)

    # Colourbar — uses the same cmap as the bars
    sm = cm.ScalarMappable(cmap=cmap, norm=norm)
    sm.set_array([])
    p1 = ax1.get_position()
    cbar_ax = fig.add_axes([0.55, p1.y0 - 0.016, 0.43, 0.010])
    cbar = fig.colorbar(sm, cax=cbar_ax, orientation='horizontal')
    cbar.ax.set_xlabel('coverage %', color='#888', fontsize=7)
    cbar.ax.tick_params(labelsize=7, colors='#888')
    cbar.set_ticks([0, 25, 50, 65, 75, 85, 100])
    cbar.outline.set_edgecolor('#333')

    # ── Panel 2: treemap ─────────────────────────────────────────────────
    ax2 = fig.add_subplot(gs[1])
    ax2.set_facecolor(BG_PANEL)
    ax2.set_xlim(0, 1)
    ax2.set_ylim(0, 1)
    ax2.axis('off')
    ax2.set_title('Test count treemap  (tile area ∝ test count)',
                  color='#e0e0e0', fontsize=11, loc='left',
                  pad=8, fontweight='bold')

    data_s = sorted([r for r in rows if r['tests'] > 0],
                    key=lambda r: r['tests'], reverse=True)
    if data_s:
        total  = sum(r['tests'] for r in data_s)
        sizes  = [r['tests'] / total for r in data_s]
        rects  = _squarify(sizes, 0, 0, 1, 1)

        for (rx, ry, rw, rh), row in zip(rects, data_s):
            pct  = row['cover'] or 0
            rgba = cmap(norm(pct))
            bg   = rgba[:3]
            fg   = text_colour(bg)
            rect = patches.FancyBboxPatch(
                (rx + 0.003, ry + 0.003),
                max(0, rw - 0.006), max(0, rh - 0.006),
                boxstyle='round,pad=0.003',
                linewidth=0.6,
                edgecolor=BG_DARK,
                facecolor=bg,
                zorder=2
            )
            ax2.add_patch(rect)
            short   = row['pkg'].split('/')[-1]
            pct_str = (f'{row["cover"]:.0f}%' if row['cover'] is not None
                       else '?')
            label   = f'{short}\n{row["tests"]}t · {pct_str}'
            fs      = min(9, max(5, rw * 55))
            if rw > 0.06 and rh > 0.05:
                ax2.text(rx + rw / 2, ry + rh / 2, label,
                         ha='center', va='center',
                         fontsize=fs, color=fg,
                         fontfamily='monospace',
                         fontweight='bold', zorder=3,
                         linespacing=1.3)

    suffix = Path(output_path).suffix.lower()
    if suffix not in ('.svg', '.png', '.pdf'):
        print(f'ERROR: unsupported format {suffix!r} — use .svg, .png, or .pdf',
              file=sys.stderr)
        sys.exit(1)

    dpi = 150 if suffix == '.png' else None
    fig.savefig(output_path, dpi=dpi,
                facecolor=fig.get_facecolor(),
                bbox_inches='tight')
    plt.close(fig)
    print(f'Charts written to {output_path}')


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def parse_coverprofile(path):
    """
    Parse a go coverprofile and return {pkg: (total_stmts, uncovered_stmts)}.
    The coverprofile line format is:
        file.go:L1.C1,L2.C2  numstmts  execcount
    numstmts is the number of statements in the block; execcount 0 = uncovered.
    """
    from collections import defaultdict
    total   = defaultdict(int)
    covered = defaultdict(int)
    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if line.startswith('mode:') or not line:
                    continue
                parts = line.rsplit(' ', 2)
                if len(parts) != 3:
                    continue
                loc, nstmts, execcount = parts
                pkg = loc.split(':')[0].rsplit('/', 1)[0]
                pkg = pkg.replace('github.com/ha1tch/xolu/', '')
                n = int(nstmts)
                total[pkg]   += n
                covered[pkg] += n if int(execcount) > 0 else 0
    except OSError:
        return {}
    return {pkg: (total[pkg], total[pkg] - covered[pkg])
            for pkg in total}


def main():
    ap = argparse.ArgumentParser(
        description='xolu test-suite visualiser',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__
    )
    ap.add_argument('mode', nargs='?', default='both',
                    choices=['heatmap', 'treemap', 'both'])
    ap.add_argument('--from-go-output', action='store_true',
                    help='Parse go test -v output from stdin')
    ap.add_argument('--output', metavar='FILE',
                    help='Write to FILE (.svg, .png, .pdf) instead of terminal')
    args = ap.parse_args()

    rows = (parse_go_output(sys.stdin) if args.from_go_output
            else parse_pipe(sys.stdin))
    if not rows:
        sys.exit(0)

    if args.output:
        export_charts(rows, args.output)
        return

    if _COLOUR_MODE == 'none':
        sys.exit(0)

    w, _ = term_size()
    w = min(w, 140)

    if args.mode in ('heatmap', 'both'):
        terminal_heatmap(rows, w)
    if args.mode in ('treemap', 'both'):
        terminal_treemap(rows, w)


if __name__ == '__main__':
    main()
