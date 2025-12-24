#!/usr/bin/env python3
"""
Утилита для расчёта метрик по результатам извлечения текста.
Позволяет сравнить вклад таблиц и базового текста в итоговый файл.
"""
from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path
from statistics import mean
from typing import Iterable, List, Sequence


@dataclass
class FileMetrics:
    path: Path
    size_bytes: int
    characters: int
    lines: int
    non_empty_lines: int
    paragraphs: int


@dataclass
class OverlapMetrics:
    base_count: int
    overlap_count: int
    percent: float


@dataclass
class TableMetrics:
    table_name: str
    column_count: int


@dataclass
class Report:
    base: FileMetrics
    tables: FileMetrics
    merged: FileMetrics
    table_metrics: List[TableMetrics]
    line_overlap: OverlapMetrics
    paragraph_overlap: OverlapMetrics
    merged_line_overlap: OverlapMetrics
    merged_paragraph_overlap: OverlapMetrics


NORMALIZE_SPACES_RE = re.compile(r"\s+")
PARAGRAPH_SPLIT_RE = re.compile(r"\n\s*\n", flags=re.MULTILINE)


def normalize_text(text: str) -> str:
    return NORMALIZE_SPACES_RE.sub(" ", text.strip()).lower()


def load_file_metrics(path: Path) -> FileMetrics:
    content = path.read_text(encoding="utf-8")
    lines = content.splitlines()
    non_empty_lines = [ln for ln in lines if ln.strip()]
    paragraphs = [p for p in PARAGRAPH_SPLIT_RE.split(content) if p.strip()]
    return FileMetrics(
        path=path,
        size_bytes=path.stat().st_size,
        characters=len(content),
        lines=len(lines),
        non_empty_lines=len(non_empty_lines),
        paragraphs=len(paragraphs),
    )


def normalized_lines(text: str) -> List[str]:
    return [normalize_text(line) for line in text.splitlines() if line.strip()]


def normalized_paragraphs(text: str) -> List[str]:
    return [normalize_text(p) for p in PARAGRAPH_SPLIT_RE.split(text) if p.strip()]


def calculate_overlap(left: Sequence[str], right: Sequence[str]) -> OverlapMetrics:
    if not left:
        return OverlapMetrics(0, 0, 0.0)
    left_set = set(left)
    right_set = set(right)
    overlap = left_set & right_set
    percent = round(len(overlap) / len(left_set) * 100, 2)
    return OverlapMetrics(len(left_set), len(overlap), percent)


def parse_tables(raw_tables: str) -> List[TableMetrics]:
    tables: List[TableMetrics] = []
    current_lines: List[str] = []
    current_name: str | None = None

    def flush_table():
        if current_name is None:
            return
        column_count = calculate_columns(current_lines)
        tables.append(TableMetrics(current_name, column_count))

    for line in raw_tables.splitlines():
        heading = line.strip()
        if heading.startswith("### table_"):
            flush_table()
            current_lines = []
            current_name = heading.lstrip("# ")
            continue
        current_lines.append(line)

    flush_table()
    return tables


def calculate_columns(table_lines: Iterable[str]) -> int:
    column_count = 0
    for line in table_lines:
        if "|" not in line:
            continue
        raw = line.strip()
        cleaned = raw.replace("|", "").replace("-", "").replace(":", "").strip()
        if cleaned == "":
            # Разделители заголовков таблиц
            continue
        cells = [cell.strip() for cell in raw.split("|") if cell.strip()]
        if cells:
            column_count = max(column_count, len(cells))
    return column_count


def render_markdown(report: Report, run_dir: Path) -> str:
    lines = [
        f"# Отчёт по директории `{run_dir}`",
        "",
        "## Размеры файлов",
        "| Файл | Размер, байт | Символов | Строк | Непустых строк | Абзацев |",
        "| --- | --- | --- | --- | --- | --- |",
        f"| {report.base.path.name} | {report.base.size_bytes} | {report.base.characters} | {report.base.lines} | {report.base.non_empty_lines} | {report.base.paragraphs} |",
        f"| {report.tables.path.name} | {report.tables.size_bytes} | {report.tables.characters} | {report.tables.lines} | {report.tables.non_empty_lines} | {report.tables.paragraphs} |",
        f"| {report.merged.path.name} | {report.merged.size_bytes} | {report.merged.characters} | {report.merged.lines} | {report.merged.non_empty_lines} | {report.merged.paragraphs} |",
        "",
        "## Таблицы",
        f"Всего таблиц: **{len(report.table_metrics)}**.",
    ]

    if report.table_metrics:
        column_counts = [tbl.column_count for tbl in report.table_metrics]
        lines.extend(
            [
                f"Среднее число колонок: **{mean(column_counts):.2f}**, минимум: **{min(column_counts)}**, максимум: **{max(column_counts)}**.",
                "",
                "| Таблица | Колонок |",
                "| --- | --- |",
            ]
        )
        for tbl in report.table_metrics:
            lines.append(f"| {tbl.table_name} | {tbl.column_count} |")
        lines.append("")

    lines.extend(
        [
            "## Совпадения base.md и tables.md",
            f"Совпадающих уникальных строк: **{report.line_overlap.overlap_count}** из {report.line_overlap.base_count} (строки base.md), совпадение **{report.line_overlap.percent}%**.",
            f"Совпадающих абзацев: **{report.paragraph_overlap.overlap_count}** из {report.paragraph_overlap.base_count}, совпадение **{report.paragraph_overlap.percent}%**.",
            "",
            "## Доля таблиц в merged.md",
            f"Совпадающих строк merged.md с tables.md: **{report.merged_line_overlap.overlap_count}** из {report.merged_line_overlap.base_count} (уникальные строки merged.md), совпадение **{report.merged_line_overlap.percent}%**.",
            f"Совпадающих абзацев merged.md и tables.md: **{report.merged_paragraph_overlap.overlap_count}** из {report.merged_paragraph_overlap.base_count}, совпадение **{report.merged_paragraph_overlap.percent}%**.",
            "",
            "Метрики считаются по нормализованным значениям (нижний регистр, схлопнутые пробелы, пустые строки игнорируются).",
        ]
    )
    return "\n".join(lines)


def build_report(run_dir: Path) -> Report:
    base_path = run_dir / "base.md"
    tables_path = run_dir / "tables.md"
    merged_path = run_dir / "merged.md"

    for path in (base_path, tables_path, merged_path):
        if not path.exists():
            raise FileNotFoundError(f"Не найден файл {path}")

    base_content = base_path.read_text(encoding="utf-8")
    tables_content = tables_path.read_text(encoding="utf-8")
    merged_content = merged_path.read_text(encoding="utf-8")

    return Report(
        base=load_file_metrics(base_path),
        tables=load_file_metrics(tables_path),
        merged=load_file_metrics(merged_path),
        table_metrics=parse_tables(tables_content),
        line_overlap=calculate_overlap(normalized_lines(base_content), normalized_lines(tables_content)),
        paragraph_overlap=calculate_overlap(normalized_paragraphs(base_content), normalized_paragraphs(tables_content)),
        merged_line_overlap=calculate_overlap(normalized_lines(merged_content), normalized_lines(tables_content)),
        merged_paragraph_overlap=calculate_overlap(normalized_paragraphs(merged_content), normalized_paragraphs(tables_content)),
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Отчёт о доле таблиц в итоговом тексте")
    parser.add_argument("run_dir", type=Path, help="Директория с файлами base.md, tables.md и merged.md")
    parser.add_argument(
        "--output",
        type=Path,
        help="Путь для сохранения отчёта (Markdown). По умолчанию выводится в stdout",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    report = build_report(args.run_dir)
    markdown = render_markdown(report, args.run_dir)
    if args.output:
        args.output.write_text(markdown, encoding="utf-8")
    print(markdown)


if __name__ == "__main__":
    main()
