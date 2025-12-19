from __future__ import annotations

import re
from collections import defaultdict
from typing import Dict, Iterable, List, Optional, Tuple

import fitz  # PyMuPDF

from .types import DocumentResult, Table


def _norm_spaces(s: str) -> str:
    """Collapse whitespace to single spaces."""
    return re.sub(r"\s+", " ", s).strip()


def _cluster_positions(values: List[float], tol: float) -> List[float]:
    """Cluster numeric positions into distinct lines.

    Values are assumed to be 1D positions (x or y). This groups nearby values
    (within `tol`) and returns the mean of each group.
    """
    if not values:
        return []

    values = sorted(values)
    clusters: List[float] = []
    bucket: List[float] = [values[0]]

    for v in values[1:]:
        if abs(v - bucket[-1]) <= tol:
            bucket.append(v)
        else:
            clusters.append(sum(bucket) / len(bucket))
            bucket = [v]

    clusters.append(sum(bucket) / len(bucket))
    return clusters


def _is_rgb_tuple(x: object) -> bool:
    return isinstance(x, tuple) and len(x) == 3 and all(isinstance(c, (int, float)) for c in x)


def _is_grey_fill(fill: object, target: float = 0.8, tol: float = 0.08) -> bool:
    """True if fill is roughly a light grey used for table grid lines."""
    if not _is_rgb_tuple(fill):
        return False
    return all(abs(float(c) - target) <= tol for c in fill)  # type: ignore[arg-type]


def _is_black_fill(fill: object, tol: float = 0.05) -> bool:
    """True if fill is roughly black."""
    if not _is_rgb_tuple(fill):
        return False
    return all(abs(float(c) - 0.0) <= tol for c in fill)  # type: ignore[arg-type]


# Signature (sequence of drawing commands) for a checkmark in these PDFs.
_TICK_CMD_PATTERNS = {
    "lclcllclclcl",
}


def extract_table_grid_lines(
    page: fitz.Page,
    *,
    cluster_tol: float = 1.0,
    min_cols: int = 3,
    min_rows: int = 2,
) -> Optional[Tuple[List[float], List[float]]]:
    """Detect table grid lines (x and y separators) from light-grey rectangles.

    Returns:
        (x_lines, y_lines) where each list contains monotonic positions of grid
        lines. If detection fails, returns None.
    """
    v_positions: List[float] = []
    h_positions: List[float] = []

    for d in page.get_drawings():
        if d.get("type") != "f":
            continue
        if not _is_grey_fill(d.get("fill")):
            continue

        items = d.get("items") or []
        if not items:
            continue
        # We only use plain rectangles (grid lines in these PDFs are tiny rectangles).
        if items[0][0] != "re":
            continue

        rect = items[0][1]  # Rect(x0,y0,x1,y1)
        x0, y0, x1, y1 = rect
        w = x1 - x0
        h = y1 - y0

        # Vertical grid line segment: thin and tall rectangle.
        if w < 2 and h > 5:
            v_positions.append((x0 + x1) / 2)
        # Horizontal grid line segment: thin and wide rectangle.
        if h < 2 and w > 5:
            h_positions.append((y0 + y1) / 2)

    if not v_positions or not h_positions:
        return None

    x_lines = _cluster_positions(v_positions, cluster_tol)
    y_lines = _cluster_positions(h_positions, cluster_tol)

    if len(x_lines) - 1 < min_cols:
        return None
    if len(y_lines) - 1 < min_rows:
        return None

    return x_lines, y_lines


def extract_checkmarks(page: fitz.Page) -> List[fitz.Rect]:
    """Extract bounding boxes of checkmarks (галочки) as vector drawings."""
    ticks: List[fitz.Rect] = []

    for d in page.get_drawings():
        if d.get("type") != "f":
            continue
        if not _is_black_fill(d.get("fill")):
            continue

        rect = d.get("rect")
        if rect is None:
            continue

        # In these PDFs, a checkmark is a small object.
        if not (3 < rect.width < 12 and 3 < rect.height < 12):
            continue

        cmds = "".join(it[0] for it in (d.get("items") or []))
        if cmds in _TICK_CMD_PATTERNS:
            ticks.append(rect)

    return ticks


def _find_interval(lines: List[float], value: float) -> Optional[int]:
    """Return i such that lines[i] <= value <= lines[i+1]."""
    for i in range(len(lines) - 1):
        if lines[i] <= value <= lines[i + 1]:
            return i
    return None


def _cell_index(x_lines: List[float], y_lines: List[float], x: float, y: float) -> Tuple[Optional[int], Optional[int]]:
    col = _find_interval(x_lines, x)
    row = _find_interval(y_lines, y)
    return row, col


def build_table_from_page(page: fitz.Page) -> Optional[dict]:
    """Parse a single page table.

    Returns a dict with keys:
        headers: list[str]
        rows: list[list[str]]
        x_lines, y_lines: detected grid lines

    If a table grid is not detected, returns None.
    """
    grid = extract_table_grid_lines(page, min_cols=3, min_rows=2)
    if grid is None:
        return None

    x_lines, y_lines = grid

    words = page.get_text("words")
    # (row, col) -> list[(y, x, text)]
    cell_words: Dict[Tuple[int, int], List[Tuple[float, float, str]]] = defaultdict(list)

    for x0, y0, x1, y1, text, *_ in words:
        cx = (x0 + x1) / 2
        cy = (y0 + y1) / 2
        row, col = _cell_index(x_lines, y_lines, cx, cy)
        if row is None or col is None:
            continue
        cell_words[(row, col)].append((cy, cx, str(text)))

    cell_text: Dict[Tuple[int, int], str] = {}
    for key, items in cell_words.items():
        items_sorted = sorted(items, key=lambda t: (t[0], t[1]))
        joined = " ".join(t[2] for t in items_sorted)
        cell_text[key] = _norm_spaces(joined)

    n_rows = len(y_lines) - 1
    n_cols = len(x_lines) - 1

    headers = [cell_text.get((0, c), "") for c in range(n_cols)]

    # Detect checkmarks and map them to cells.
    ticks = extract_checkmarks(page)
    tick_cells: Dict[Tuple[int, int], int] = defaultdict(int)
    for r in ticks:
        cx = (r.x0 + r.x1) / 2
        cy = (r.y0 + r.y1) / 2
        row, col = _cell_index(x_lines, y_lines, cx, cy)
        if row is None or col is None:
            continue
        tick_cells[(row, col)] += 1

    rows: List[List[str]] = []

    for row_i in range(1, n_rows):  # skip header row
        row_out: List[str] = []
        for col_i in range(n_cols):
            if (row_i, col_i) in tick_cells:
                row_out.append("✓")
            else:
                if col_i in (0, 1):
                    row_out.append(cell_text.get((row_i, col_i), ""))
                else:
                    row_out.append("")

        # Skip completely empty data rows.
        if not row_out[0] and not row_out[1]:
            continue

        rows.append(row_out)

    return {
        "headers": headers,
        "rows": rows,
        "x_lines": x_lines,
        "y_lines": y_lines,
    }


def extract_header_text(page: fitz.Page, table_top_y: Optional[float]) -> str:
    """Extract blocks of text above the table."""
    blocks = page.get_text("blocks")

    selected = []
    for x0, y0, x1, y1, text, *_ in blocks:
        if not str(text).strip():
            continue
        if table_top_y is None:
            selected.append((y0, x0, str(text)))
        else:
            # take blocks fully above the first grid line
            if y1 <= table_top_y - 1:
                selected.append((y0, x0, str(text)))

    selected.sort(key=lambda t: (t[0], t[1]))
    joined = "\n".join(t[2].strip() for t in selected)
    joined = re.sub(r"\n{3,}", "\n\n", joined).strip()
    return joined


def parse_pdf(pdf_path: str) -> DocumentResult:
    """Parse a protocol PDF and return header text + table with checkmarks."""
    doc = fitz.open(pdf_path)

    per_page_tables = []
    for page in doc:
        t = build_table_from_page(page)
        if t:
            per_page_tables.append(t)

    # Header: use first page + first detected table on page 1 (if any)
    first_table_on_first_page = build_table_from_page(doc[0])
    table_top_y = None
    if first_table_on_first_page and first_table_on_first_page.get("y_lines"):
        table_top_y = float(first_table_on_first_page["y_lines"][0])
    header_text = extract_header_text(doc[0], table_top_y)

    if not per_page_tables:
        return DocumentResult(source_pdf=pdf_path, header_text=header_text, table=None)

    # Use headers from the first table.
    headers = per_page_tables[0]["headers"]

    # Merge rows from all pages by row number.
    all_rows: List[List[str]] = []
    for t in per_page_tables:
        all_rows.extend(t["rows"])

    # Deduplicate by the first column (row number), keep the first.
    seen = set()
    unique_rows: List[List[str]] = []

    def _row_sort_key(r: List[str]) -> int:
        # row number is usually like "70".
        m = re.search(r"\d+", r[0])
        return int(m.group(0)) if m else 0

    for r in sorted(all_rows, key=_row_sort_key):
        key = r[0].strip()
        if not key or key in seen:
            continue
        seen.add(key)
        unique_rows.append(r)

    return DocumentResult(
        source_pdf=pdf_path,
        header_text=header_text,
        table=Table(headers=headers, rows=unique_rows),
    )
