from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .common import JsonValue


@dataclass(slots=True)
class EnvironmentSpec:
    metadata: Any
    name: str | None = None
    variables: dict[str, JsonValue] = field(default_factory=dict)
    raw: dict[str, Any] = field(default_factory=dict)
