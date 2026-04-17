from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any


JsonValue = Any


class SpecKind(str, Enum):
    REQUEST_DEFINITION = "requestDefinition"
    TEST_SUITE = "testSuite"
    ENVIRONMENT = "environment"


class ResultStatus(str, Enum):
    PASSED = "passed"
    FAILED = "failed"
    SKIPPED = "skipped"


class VariableScope(str, Enum):
    SUITE = "suite"
    TEST = "test"
    STEP = "step"


@dataclass(slots=True)
class SourceRef:
    file: Path
    document_path: str | None = None
    line: int | None = None
    column: int | None = None


@dataclass(slots=True)
class SpecMetadata:
    format_version: str
    kind: SpecKind
    id: str | None = None
    name: str | None = None
    description: str | None = None
    source: SourceRef | None = None


@dataclass(slots=True)
class TimingMetrics:
    total_ms: float | None = None
    avg_ms: float | None = None
    p95_ms: float | None = None
    max_ms: float | None = None


@dataclass(slots=True)
class RetrySummary:
    attempts: int = 0
    retried: bool = False
    reasons: list[str] = field(default_factory=list)
