#!/usr/bin/env python3
"""Generate the product Live view from the designated prototype template."""

from pathlib import Path


def main():
    root = Path(__file__).resolve().parents[1]
    template = root / "docs/samples/live-semantic-map-prototype.html"
    source = template.read_text()
    marker = "// SAMPLE_BOOTSTRAP\n"
    if source.count(marker) != 1:
        raise SystemExit("Expected exactly one SAMPLE_BOOTSTRAP in the template")
    renderer, sample = source.split(marker)
    closing = "</script>\n</body>\n</html>\n"
    if not sample.endswith(closing):
        raise SystemExit("Unexpected template closing markup")
    product = renderer + "// LIVE_BOOTSTRAP\nboot();\n" + closing
    (root / "internal/flowview/live_view.html").write_text(product)


if __name__ == "__main__":
    main()
