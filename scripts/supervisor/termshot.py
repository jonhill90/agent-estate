#!/usr/bin/env python3
"""Render a tmux pane's ANSI frame to SVG, so an agent can SEE a TUI.

There is no Playwright for the terminal. But `tmux capture-pane -p -e` returns
the exact rendered frame including truecolor escapes -- that IS the DOM. This
turns it into an image an agent can view.

#110: this is the verified rasteriser posted on the issue -- built and proved
against a real pane (it caught #109's ragged-selection-width defect visually,
confirming what the escape-code diagnosis already found). Copied in as-is
rather than rewritten. `look.py`'s `png` command is what calls `render()` and
carries it the rest of the way to a screenshot via headless Chrome.
"""
import re, subprocess, sys, html

FG = re.compile(r'\x1b\[38;2;(\d+);(\d+);(\d+)m')
BG = re.compile(r'\x1b\[48;2;(\d+);(\d+);(\d+)m')
SGR = re.compile(r'\x1b\[([0-9;]*)m')

def cells(line):
    """Yield (char, fg, bg, bold, reverse) walking the escape sequences."""
    fg, bg, bold, rev = "#c0c0c0", None, False, False
    i = 0
    while i < len(line):
        m = SGR.match(line, i)
        if m:
            body = m.group(0)
            f, b = FG.match(body), BG.match(body)
            if f: fg = "#%02x%02x%02x" % tuple(int(x) for x in f.groups())
            elif b: bg = "#%02x%02x%02x" % tuple(int(x) for x in b.groups())
            else:
                for c in (m.group(1) or "0").split(';'):
                    if c in ("0", ""): fg, bg, bold, rev = "#c0c0c0", None, False, False
                    elif c == "1": bold = True
                    elif c == "7": rev = True
                    elif c == "27": rev = False
                    elif c == "39": fg = "#c0c0c0"
                    elif c == "49": bg = None
            i = m.end(); continue
        yield line[i], fg, bg, bold, rev
        i += 1

def render(target, out):
    raw = subprocess.run(["tmux","capture-pane","-p","-e","-t",target],
                         capture_output=True, text=True).stdout
    lines = raw.split("\n")
    CW, CH = 8.4, 17
    W = max((len(SGR.sub("", l)) for l in lines), default=80)
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{int(W*CW)+16}" '
           f'height="{len(lines)*CH+16}" font-family="Menlo,monospace" font-size="14">',
           f'<rect width="100%" height="100%" fill="#1a1b26"/>']
    for row, line in enumerate(lines):
        y = row*CH + 20
        for col,(ch,fg,bg,bold,rev) in enumerate(cells(line)):
            if ch == " " and not bg and not rev: continue
            x = col*CW + 8
            f, b = (bg or "#1a1b26", fg) if rev else (fg, bg)
            if b: svg.append(f'<rect x="{x:.1f}" y="{y-13}" width="{CW:.1f}" height="{CH}" fill="{b}"/>')
            if ch != " ":
                w = ' font-weight="bold"' if bold else ''
                svg.append(f'<text x="{x:.1f}" y="{y}" fill="{f}"{w}>{html.escape(ch)}</text>')
    svg.append("</svg>")
    open(out,"w").write("\n".join(svg))
    return out, len(lines), W

if __name__ == "__main__":
    o,h,w = render(sys.argv[1], sys.argv[2])
    print(f"  rendered {w}x{h} cells -> {o}")
