from __future__ import annotations

from typing import Iterable

from .types import DocumentResult, Table


def _escape_cell(text: str) -> str:
    """Escape Markdown table separators."""
    return str(text).replace("|", "\\|")


def render_table_md(table: Table) -> str:
    headers = [_escape_cell(h) for h in table.headers]
    lines = []
    lines.append("| " + " | ".join(headers) + " |")
    lines.append("| " + " | ".join(["---"] * len(headers)) + " |")

    for row in table.rows:
        row_cells = [_escape_cell(c) for c in row]
        # pad if needed
        if len(row_cells) < len(headers):
            row_cells += [""] * (len(headers) - len(row_cells))
        lines.append("| " + " | ".join(row_cells) + " |")

    return "\n".join(lines)


def render_document_md(doc: DocumentResult) -> str:
    parts = []
    if doc.header_text:
        parts.append(doc.header_text.strip())
    if doc.table:
        parts.append(render_table_md(doc.table))
    return "\n\n".join(p for p in parts if p)
