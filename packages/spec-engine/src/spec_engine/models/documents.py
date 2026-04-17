from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .environment import EnvironmentSpec
from .request import RequestDefinitionSpec
from .suite import TestSuiteSpec


@dataclass(slots=True)
class LoadedDocument:
    path: Path
    raw: dict[str, Any]
    typed: RequestDefinitionSpec | TestSuiteSpec | EnvironmentSpec
    source_map: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class LoadedBundle:
    suite: LoadedDocument
    environment: LoadedDocument | None = None
    referenced_requests: dict[Path, LoadedDocument] = field(default_factory=dict)
