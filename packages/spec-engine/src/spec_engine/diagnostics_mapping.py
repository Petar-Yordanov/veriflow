from __future__ import annotations

from pathlib import Path
from typing import Any

from .diagnostics import DocumentLocation


def lookup_location(source_map: dict[str, Any], file: Path, path: str | None) -> DocumentLocation:
    if path and path in source_map:
        line, column = source_map[path]
        return DocumentLocation(file=file, document_path=path, line=line, column=column)
    return DocumentLocation(file=file, document_path=path)
