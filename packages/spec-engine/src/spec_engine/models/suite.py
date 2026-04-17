from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .assertions import ExpectationSpec
from .common import JsonValue, SourceRef, VariableScope
from .request import RequestSpec


@dataclass(slots=True)
class WaitSpec:
    before_ms: int | None = None
    after_ms: int | None = None
    for_ms: int | None = None


@dataclass(slots=True)
class RetryCondition:
    status_in: list[int] = field(default_factory=list)


@dataclass(slots=True)
class RetrySpec:
    count: int = 0
    delay_ms: int = 0
    when: RetryCondition = field(default_factory=RetryCondition)


@dataclass(slots=True)
class RepeatSpec:
    warmup_count: int = 0
    count: int = 1


@dataclass(slots=True)
class LogSideSpec:
    headers: bool | None = None
    body: bool | None = None


@dataclass(slots=True)
class LogSpec:
    request: LogSideSpec = field(default_factory=LogSideSpec)
    response: LogSideSpec = field(default_factory=LogSideSpec)


@dataclass(slots=True)
class ArtifactSpec:
    save_response_body_to: str | None = None
    save_parsed_json_to: str | None = None
    save_headers_to: str | None = None
    save_timing_to: str | None = None


@dataclass(slots=True)
class ExtractionSpec:
    from_selector: str | None = None
    from_definition: str | None = None
    scope: VariableScope = VariableScope.TEST
    required: bool = False
    sensitive: bool = False


@dataclass(slots=True)
class StepSpec:
    id: str | None = None
    name: str | None = None
    skip: bool = False
    continue_on_failure: bool = False
    variables: dict[str, JsonValue] = field(default_factory=dict)
    wait: WaitSpec | None = None
    use: str | None = None
    with_: dict[str, JsonValue] = field(default_factory=dict)
    request: RequestSpec | None = None
    extend: dict[str, JsonValue] = field(default_factory=dict)
    overrides: dict[str, JsonValue] = field(default_factory=dict)
    timeout_ms: int | None = None
    expect: ExpectationSpec | None = None
    extract: dict[str, ExtractionSpec] = field(default_factory=dict)
    retry: RetrySpec | None = None
    repeat: RepeatSpec | None = None
    log: LogSpec | None = None
    artifacts: ArtifactSpec | None = None
    source: SourceRef | None = None


@dataclass(slots=True)
class TestSpec:
    id: str
    name: str | None = None
    tags: list[str] = field(default_factory=list)
    skip: bool = False
    skip_reason: str | None = None
    variables: dict[str, JsonValue] = field(default_factory=dict)
    steps: list[StepSpec] = field(default_factory=list)
    source: SourceRef | None = None


@dataclass(slots=True)
class SuiteInfo:
    name: str | None = None
    description: str | None = None


@dataclass(slots=True)
class GlobalsSpec:
    variables: dict[str, JsonValue] = field(default_factory=dict)


@dataclass(slots=True)
class TestSuiteSpec:
    metadata: Any
    info: SuiteInfo = field(default_factory=SuiteInfo)
    globals: GlobalsSpec = field(default_factory=GlobalsSpec)
    tests: list[TestSpec] = field(default_factory=list)
    raw: dict[str, Any] = field(default_factory=dict)
