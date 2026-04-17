from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .common import JsonValue, SourceRef


@dataclass(slots=True)
class AssertionClause:
    path: str | None = None
    element_field: str | None = None
    operators: dict[str, JsonValue] = field(default_factory=dict)
    and_: list["AssertionClause"] = field(default_factory=list)
    or_: list["AssertionClause"] = field(default_factory=list)
    source: SourceRef | None = None


@dataclass(slots=True)
class HeaderExpectation:
    operators: dict[str, JsonValue] = field(default_factory=dict)


@dataclass(slots=True)
class TextExpectation:
    operators: dict[str, JsonValue] = field(default_factory=dict)


@dataclass(slots=True)
class BinaryExpectation:
    operators: dict[str, JsonValue] = field(default_factory=dict)


@dataclass(slots=True)
class PerformanceExpectation:
    metrics: dict[str, dict[str, JsonValue]] = field(default_factory=dict)


@dataclass(slots=True)
class ExpectationSpec:
    status: int | list[int] | None = None
    body: AssertionClause | None = None
    headers: dict[str, HeaderExpectation] = field(default_factory=dict)
    text: TextExpectation | None = None
    binary: BinaryExpectation | None = None
    performance: PerformanceExpectation | None = None
