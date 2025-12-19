from __future__ import annotations

from dataclasses import dataclass
from typing import List, Optional


@dataclass(frozen=True)
class Table:
    headers: List[str]
    rows: List[List[str]]


@dataclass(frozen=True)
class DocumentResult:
    """Parsed document result."""

    source_pdf: str
    header_text: str
    table: Optional[Table]
