from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from ..constants import SUPPORTED_SPEC_MAJOR_VERSIONS
from ..diagnostics import Diagnostic, DiagnosticSeverity
from .base import SpecVersionHandler
from .v1.handler import V1SpecHandler


@dataclass(slots=True)
class VersionRegistry:
    handlers: list[SpecVersionHandler] = field(default_factory=list)

    def register(self, handler: SpecVersionHandler) -> None:
        self.handlers.append(handler)

    def handler_for_document(self, raw: dict[str, Any]) -> SpecVersionHandler:
        fmt = str(raw.get("formatVersion", ""))
        for handler in self.handlers:
            if handler.supports_format(fmt):
                return handler
        raise ValueError(f"Unsupported formatVersion: {fmt}")


def build_default_registry() -> VersionRegistry:
    registry = VersionRegistry()
    registry.register(V1SpecHandler())
    return registry
