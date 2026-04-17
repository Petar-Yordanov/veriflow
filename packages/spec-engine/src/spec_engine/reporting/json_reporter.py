from __future__ import annotations

import json
from dataclasses import asdict
from pathlib import Path


class JsonFileReporter:
    def __init__(self, output_path: Path) -> None:
        self.output_path = output_path
        self._events: list[dict] = []

    async def on_event(self, event) -> None:
        self._events.append(event.to_dict())

    async def finalize(self, result) -> None:
        payload = {"events": self._events, "result": asdict(result)}
        self.output_path.parent.mkdir(parents=True, exist_ok=True)
        self.output_path.write_text(json.dumps(payload, indent=2), encoding="utf-8")
