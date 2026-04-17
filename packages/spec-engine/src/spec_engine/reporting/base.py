from __future__ import annotations

from typing import Protocol

from ..events.base import EngineEvent
from ..models.results import SuiteResult


class Reporter(Protocol):
    async def on_event(self, event: EngineEvent) -> None: ...
    async def finalize(self, result: SuiteResult) -> None: ...
