from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any


class DiagnosticSeverity(str, Enum):
    ERROR = "error"
    WARNING = "warning"
    INFO = "info"


@dataclass(slots=True, frozen=True)
class DocumentLocation:
    file: Path | None = None
    document_path: str | None = None
    line: int | None = None
    column: int | None = None


@dataclass(slots=True, frozen=True)
class Diagnostic:
    code: str
    message: str
    severity: DiagnosticSeverity
    location: DocumentLocation = field(default_factory=DocumentLocation)
    details: dict[str, Any] = field(default_factory=dict)

    @property
    def is_error(self) -> bool:
        return self.severity == DiagnosticSeverity.ERROR
