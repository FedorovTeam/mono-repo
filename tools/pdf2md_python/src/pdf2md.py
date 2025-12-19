"""Convenience entrypoint.

Usage:
    python pdf2md.py <input.pdf> -o out.md
    python pdf2md.py <dir_with_pdfs> --out-dir out
"""

from __future__ import annotations

import sys
from pathlib import Path

# Make sure `src/` is on sys.path when running from repo root.
ROOT = Path(__file__).resolve().parent
SRC = ROOT / "src"
sys.path.insert(0, str(SRC))

from iltumen_pdf2md.cli import main  # noqa: E402


if __name__ == "__main__":
    raise SystemExit(main())
