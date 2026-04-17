from __future__ import annotations

from pathlib import Path
from typing import Any, Protocol

from ..diagnostics import Diagnostic


class SpecVersionHandler(Protocol):
    major_version: int

    def supports_format(self, format_version: str) -> bool: ...

    def parse_document(self, path: Path, raw: dict[str, Any], source_map: dict[str, Any]) -> Any: ...

    def validate_document(self, document: Any, raw: dict[str, Any], file: Path, source_map: dict[str, Any]) -> list[Diagnostic]: ...
