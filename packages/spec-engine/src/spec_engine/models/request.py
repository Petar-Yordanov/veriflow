from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .common import JsonValue, SourceRef


@dataclass(slots=True)
class InputDefinition:
    type: str | None = None
    required: bool = False
    sensitive: bool = False
    description: str | None = None
    source: SourceRef | None = None


@dataclass(slots=True)
class OutputDefinition:
    path: str
    required: bool = False
    sensitive: bool = False
    source: SourceRef | None = None


@dataclass(slots=True)
class PathParamEncoding:
    enabled: bool = False


@dataclass(slots=True)
class RequestSpec:
    method: str
    url: str | None = None
    base_url: str | None = None
    path: str | None = None
    path_params: dict[str, JsonValue] = field(default_factory=dict)
    path_param_encoding: PathParamEncoding = field(default_factory=PathParamEncoding)
    query: dict[str, JsonValue] = field(default_factory=dict)
    headers: dict[str, JsonValue] = field(default_factory=dict)
    body: JsonValue | None = None
    body_raw: str | None = None
    body_file: str | None = None
    body_file_mode: str | None = None
    form: dict[str, JsonValue] = field(default_factory=dict)
    multipart: dict[str, JsonValue] = field(default_factory=dict)
    timeout_ms: int | None = None
    follow_redirects: bool | None = None
    source: SourceRef | None = None


@dataclass(slots=True)
class RequestDefinitionSpec:
    metadata: Any
    request: RequestSpec
    inputs: dict[str, InputDefinition] = field(default_factory=dict)
    outputs: dict[str, OutputDefinition] = field(default_factory=dict)
    raw: dict[str, Any] = field(default_factory=dict)
