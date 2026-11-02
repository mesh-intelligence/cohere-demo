# Copyright (c) 2026 Mesh Intelligence Inc.
# SPDX-License-Identifier: MIT-0
#
# Generates the deck's workflow figures (GH-617): one d2 template renders the
# base flow per focus variant, and the Cohere model ovals are injected below
# their steps afterwards, because d2's layout engines pin nothing and a
# container-based attempt let the ovals wander. Run from talks/figures:
#
#   python3 build_workflow.py
#
# The step labels are multi-line so the boxes stay narrow enough to read at
# slide width, and the ovals carry only the model name.

import base64
import re
import subprocess

# Steps in flow order: (id, label). Multi-line labels keep boxes narrow.
NODES = [
    ("select", "Choose which\nsources to search"),
    ("search", "Search\nthe corpus"),
    ("s1", "HUMINT"),
    ("s2", "OSINT"),
    ("s3", "SIGINT"),
    ("rerank", "Pick the\nbest sources"),
    ("command", "Write brief\nwith citations"),
    ("check", "Check the\ntemplate\n(no model)"),
]

# Cohere models hanging under steps: step id -> (oval label, link).
# Family names only, no version dates: selection and composition run the
# flagship (cohere.model, Command A), the audit judge runs the classifier
# tier (classifierModel, Command R).
MODELS = {
    "select": ("Command A", "https://cohere.com/command"),
    "search": ("Embed", "https://cohere.com/embed"),
    "rerank": ("Rerank", "https://cohere.com/rerank"),
    "command": ("Command A", "https://cohere.com/command"),
    "audit": ("Command R", "https://cohere.com/command"),
}

EDGES = [
    ("tasking", "focus.embed"),
    ("focus.embed", "focus.select"),
    ("focus.select", "focus.search"),
    ("focus.search", "focus.s1"),
    ("focus.search", "focus.s2"),
    ("focus.search", "focus.s3"),
    ("focus.s1", "focus.rerank"),
    ("focus.s2", "focus.rerank"),
    ("focus.s3", "focus.rerank"),
    ("focus.rerank", "focus.command"),
    ("focus.command", "focus.check"),
    ("focus.check", "intsum"),
]

# Figure name -> the step ids inside the dashed focus box.
VARIANTS = {
    "workflow": ["select", "search", "s1", "s2", "s3", "rerank", "command", "check"],
    "wf-make-brief": ["select", "search", "s1", "s2", "s3", "rerank", "command", "check"],
    "wf-revise": ["select", "search", "s1", "s2", "s3", "rerank", "command", "check"],
    "wf-search": ["select", "search", "s1", "s2", "s3"],
    "wf-harvest": ["search", "s1", "s2", "s3"],
    "wf-summarize": ["command"],
    "wf-translate": ["command"],
    "wf-eval": ["select", "search", "s1", "s2", "s3", "rerank"],
    "wf-audit": ["intsum", "audit"],
}

# The refine loop (GH-623) is its own compact figure: the full pipeline lives
# on the neighboring slides, and at pipeline width the loop's text would be
# unreadable at slide scale.
REFINE_D2 = """direction: right

vars: {
  d2-config: {
    theme-id: 0
    pad: 6
  }
}

classes: {
  step: {
    style: {
      border-radius: 8
      fill: "#f6f8fb"
      stroke: "#7a8699"
      font-size: 19
    }
  }
}

(* -> *)[*].style.stroke: "#7a8699"
intsum: Cited\\nINTSUM {
  shape: page
  style: {fill: "#eef6ef"; stroke: "#2f7d4f"; font-size: 19}
}
audit: Audit every\\ncitation {
  class: step
}
findings: Audit\\nfindings {
  shape: page
  style: {fill: "#fbfcfe"; stroke: "#3d70b8"; font-size: 19}
}
rerun: The whole workflow\\nre-runs on the findings {
  class: step
}
intsum -> audit
audit -> findings: scored 1-10 {style.font-size: 16}
findings -> rerun: the next prompt {style.font-size: 16}
rerun -> intsum: a new cited INTSUM {style.stroke-dash: 4; style.font-size: 16}
"""

DROP = 100  # arrow + oval vertical budget under a step
OVAL_RY = 27


def build(name: str, focus: list[str]) -> str:
    lines = [
        "direction: right",
        "",
        "vars: {",
        "  d2-config: {",
        "    theme-id: 0",
        "    pad: 6",
        "  }",
        "}",
        "",
        "classes: {",
        "  step: {",
        "    style: {",
        "      border-radius: 8",
        '      fill: "#f6f8fb"',
        '      stroke: "#7a8699"',
        "      font-size: 19",
        "    }",
        "  }",
        "}",
        "",
        '(* -> *)[*].style.stroke: "#7a8699"',
    ]

    def declare(node_id: str, label: str, indent: str) -> list[str]:
        escaped = label.replace("\n", "\\n")
        block = [f"{indent}{node_id}: {escaped} {{", f"{indent}  class: step"]
        if node_id in ("s1", "s2", "s3", "check"):
            block.append(f"{indent}    style.font-size: 16")
        block.append(f"{indent}}}")
        return block

    def page(node_id: str, label: str, fill: str, stroke: str, indent: str = "") -> list[str]:
        escaped = label.replace("\n", "\\n")
        return [
            f"{indent}{node_id}: {escaped} {{",
            f"{indent}  shape: page",
            f'{indent}  style: {{fill: "{fill}"; stroke: "{stroke}"; font-size: 19}}',
            f"{indent}}}",
        ]

    lines += ['focus: "" {', "  style: {", '    stroke: "#3d70b8"', "    stroke-dash: 4",
              "    fill: transparent", "    border-radius: 10", "  }"]
    for node_id, label in NODES:
        if node_id in focus:
            lines += declare(node_id, label, "  ")
    if "intsum" in focus:
        lines += page("intsum", "Cited\nINTSUM", "#eef6ef", "#2f7d4f", "  ")
    if "audit" in focus:
        audit_block = declare("audit", "Audit\ncitations", "  ")
        audit_block.insert(len(audit_block) - 1, "    style.stroke-dash: 4")
        lines += audit_block
    lines.append("}")

    for node_id, label in NODES:
        if node_id not in focus:
            lines += declare(node_id, label, "")
    lines += page("tasking", "Tasking", "#fbfcfe", "#3d70b8")
    if "intsum" not in focus:
        lines += page("intsum", "Cited\nINTSUM", "#eef6ef", "#2f7d4f")
    # The generate slide ends its story at the cited INTSUM; the audit tail
    # lives on the audit and refine slides (GH-724).
    show_audit = name != "wf-make-brief"
    if show_audit and "audit" not in focus:
        lines += declare("audit", "Audit\ncitations", "")
        lines.insert(len(lines) - 1, "  style.stroke-dash: 4")

    def ref(node_id: str) -> str:
        return f"focus.{node_id}" if node_id in focus else node_id

    edge_pairs = [
        ("tasking", "select"), ("select", "search"),
        ("search", "s1"), ("search", "s2"), ("search", "s3"),
        ("s1", "rerank"), ("s2", "rerank"), ("s3", "rerank"),
        ("rerank", "command"), ("command", "check"), ("check", "intsum"),
    ]
    for left, right in edge_pairs:
        lines.append(f"{ref(left)} -> {ref(right)}")
    if show_audit:
        lines.append(f"{ref('intsum')} -> {ref('audit')}: optional {{style.stroke-dash: 4}}")
    if name == "wf-refine":
        lines.append(
            f"{ref('audit')} -> {ref('embed')}: findings become the next prompt "
            "{style.stroke-dash: 4; style.stroke: \"#7d33f2\"; style.font-color: \"#4b2d91\"}"
        )
    return "\n".join(lines) + "\n"


def render(name: str, source: str) -> str:
    d2_path = f"{name}.d2"
    svg_path = f"{name}.svg"
    with open(d2_path, "w") as handle:
        handle.write(source)
    subprocess.run(["d2", "--theme", "0", d2_path, svg_path], check=True)
    with open(svg_path) as handle:
        return handle.read()


def node_rect(svg: str, path: str, suffix: str = " step"):
    encoded = base64.b64encode(path.encode()).decode()
    for cls in (encoded + suffix, encoded):
        match = re.search(
            re.escape(f'<g class="{cls}"') + r".*?<(?:rect|path)[^>]*", svg, re.S
        )
        if match:
            block = match.group(0)
            rect = re.search(
                r'<rect x="([\d.]+)" y="([\d.]+)" width="([\d.]+)" height="([\d.]+)"',
                block,
            )
            if rect:
                x, y, w, h = (float(v) for v in rect.groups())
                return x, y, w, h
            # page shapes render as paths; take the text anchor instead
            text = re.search(
                re.escape(f'<g class="{cls}"')
                + r'.*?<text x="([\d.]+)" y="([\d.]+)"',
                svg,
                re.S,
            )
            if text:
                tx, ty = (float(v) for v in text.groups())
                return tx - 50, ty - 40, 100, 80
    return None


def inject(svg: str, focus: list[str]) -> str:
    view = re.search(r'viewBox="([-\d.]+) ([-\d.]+) ([\d.]+) ([\d.]+)"', svg)
    vx, vy, vw, vh = (float(v) for v in view.groups())
    additions = []
    max_right = vx + vw
    for step, (label, link) in MODELS.items():
        path = f"focus.{step}" if step in focus else step
        rect = node_rect(svg, path, " step")
        if rect is None:
            continue
        x, y, w, h = rect
        cx = x + w / 2
        top = y + h
        line_y2 = top + DROP - 2 * OVAL_RY - 10
        oval_cy = line_y2 + 10 + OVAL_RY
        rx = max(56, 10 + 9 * len(label))
        dash = ' stroke-dasharray="6,4"' if step == "audit" else ""
        additions.append(
            f'<g><line x1="{cx}" y1="{top}" x2="{cx}" y2="{line_y2}" '
            f'stroke="#7d33f2" stroke-width="2"{dash}/>'
            f'<polygon points="{cx - 6},{line_y2} {cx + 6},{line_y2} {cx},{line_y2 + 10}" fill="#7d33f2"/>'
            f'<a href="{link}" target="_blank">'
            f'<ellipse cx="{cx}" cy="{oval_cy}" rx="{rx}" ry="{OVAL_RY}" fill="#f1eafc" '
            f'stroke="#7d33f2" stroke-width="2"/>'
            f'<text x="{cx}" y="{oval_cy + 5}" text-anchor="middle" font-size="19" '
            f'font-weight="bold" fill="#4b2d91" class="text-bold">{label}</text></a></g>'
        )
        max_right = max(max_right, cx + rx + 14)
    # The Tasking page shape's stroke sat exactly on the viewBox edge at
    # pad 6 and clipped (GH-638); the injected viewBox pads left and right
    # only, so the figures gain no height on the already-tight slides.
    edge = 8
    vx -= edge
    grown_h = vh + DROP + 4
    grown_w = max_right - vx + edge
    body = svg.replace("</svg></svg>", "".join(additions) + "</svg></svg>")
    body = re.sub(
        r'viewBox="[-\d.]+ [-\d.]+ [\d.]+ [\d.]+"',
        f'viewBox="{vx} {vy} {grown_w} {grown_h}"',
        body,
        count=2,
    )
    body = re.sub(
        r'(<svg[^>]*?)width="[\d.]+" height="[\d.]+"',
        rf'\g<1>width="{grown_w}" height="{grown_h}"',
        body,
        count=2,
    )
    return body


def main() -> None:
    for name, focus in VARIANTS.items():
        source = build(name, focus)
        svg = render(name, source)
        with open(f"{name}.svg", "w") as handle:
            handle.write(inject(svg, focus))
        print(f"built {name}.svg")
    svg = render("wf-refine", REFINE_D2)
    with open("wf-refine.svg", "w") as handle:
        handle.write(inject(svg, []))
    print("built wf-refine.svg")
    # The provenance figure (GH-664) keeps its d2 as a committed source file:
    # the labels carry quotes and dots that fight string templating.
    subprocess.run(["d2", "--theme", "0", "provenance.d2", "provenance.svg"], check=True)
    print("built provenance.svg")


if __name__ == "__main__":
    main()
