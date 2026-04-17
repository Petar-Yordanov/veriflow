from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .common import ResultStatus, RetrySummary, TimingMetrics


@dataclass(slots=True)
class AssertionEvaluation:
    target: str
    operator: str
    expected: Any
    actual: Any
    passed: bool
    message: str | None = None


@dataclass(slots=True)
class ExtractionResult:
    name: str
    scope: str
    value: Any = None
    sensitive: bool = False
    missing: bool = False
    passed: bool = True
    message: str | None = None


@dataclass(slots=True)
class RequestSummary:
    method: str
    url: str
    headers: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class ResponseSummary:
    status_code: int | None = None
    headers: dict[str, str] = field(default_factory=dict)
    body_preview: str | None = None


@dataclass(slots=True)
class StepResult:
    id: str | None
    name: str | None
    status: ResultStatus
    started_at: str
    finished_at: str
    duration_ms: float
    request_summary: RequestSummary | None = None
    response_summary: ResponseSummary | None = None
    assertions: list[AssertionEvaluation] = field(default_factory=list)
    extractions: list[ExtractionResult] = field(default_factory=list)
    retry_summary: RetrySummary = field(default_factory=RetrySummary)
    timing: TimingMetrics = field(default_factory=TimingMetrics)
    error: str | None = None
    artifacts: list[str] = field(default_factory=list)


@dataclass(slots=True)
class TestResult:
    id: str
    name: str | None
    status: ResultStatus
    started_at: str
    finished_at: str
    duration_ms: float
    tags: list[str] = field(default_factory=list)
    steps: list[StepResult] = field(default_factory=list)


@dataclass(slots=True)
class SuiteResult:
    status: ResultStatus
    started_at: str
    finished_at: str
    duration_ms: float
    passed_count: int
    failed_count: int
    skipped_count: int
    tests: list[TestResult] = field(default_factory=list)
    diagnostics: list[dict[str, Any]] = field(default_factory=list)
