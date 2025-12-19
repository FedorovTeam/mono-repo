from __future__ import annotations

import argparse
import json
from dataclasses import asdict
from pathlib import Path
from typing import Optional

from .markdown import render_document_md
from .parser import parse_pdf


def _write_utf8(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def _convert_one(pdf_path: Path, out_md: Path, *, dump_json: bool = False) -> None:
    parsed = parse_pdf(str(pdf_path))
    md = render_document_md(parsed)
    _write_utf8(out_md, md)

    if dump_json:
        out_json = out_md.with_suffix(".json")
        _write_utf8(out_json, json.dumps(asdict(parsed), ensure_ascii=False, indent=2))


def build_arg_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="iltumen_pdf2md",
        description="Convert Il Tumen voting protocol PDF to Markdown (with ✓ checkmarks).",
    )

    p.add_argument(
        "input",
        type=str,
        help="Path to a .pdf file OR a directory containing .pdf files",
    )
    p.add_argument(
        "-o",
        "--output",
        type=str,
        default=None,
        help="Output .md path (only for single-file mode). Default: <input>.md",
    )
    p.add_argument(
        "--out-dir",
        type=str,
        default=None,
        help="Output directory (for directory mode). Default: <input dir>",
    )
    p.add_argument(
        "--dump-json",
        action="store_true",
        help="Also dump parsed result as .json рядом с .md (для отладки).",
    )

    return p


def main(argv: Optional[list[str]] = None) -> int:
    args = build_arg_parser().parse_args(argv)

    in_path = Path(args.input)
    if not in_path.exists():
        raise SystemExit(f"Input not found: {in_path}")

    if in_path.is_dir():
        out_dir = Path(args.out_dir) if args.out_dir else in_path
        pdfs = sorted(in_path.glob("*.pdf"))
        if not pdfs:
            raise SystemExit(f"No .pdf files found in: {in_path}")

        for pdf in pdfs:
            out_md = out_dir / f"{pdf.stem}.md"
            _convert_one(pdf, out_md, dump_json=args.dump_json)
        return 0

    # single file
    if in_path.suffix.lower() != ".pdf":
        raise SystemExit("Input must be a .pdf file or a directory with .pdf files")

    out_md = Path(args.output) if args.output else in_path.with_suffix(".md")
    _convert_one(in_path, out_md, dump_json=args.dump_json)
    return 0
