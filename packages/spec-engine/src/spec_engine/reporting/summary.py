from __future__ import annotations

from .base import Reporter


class SummaryReporter:
    def __init__(self) -> None:
        self.events: list[dict] = []
        self.final_result = None

    async def on_event(self, event) -> None:
        self.events.append(event.to_dict())

    async def finalize(self, result) -> None:
        self.final_result = result
