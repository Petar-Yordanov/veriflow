from __future__ import annotations

import json
from pathlib import Path

from rich.console import Console
from rich.text import Text
from spec_engine.events.base import EngineEvent
from spec_engine.reporting.base import Reporter


class LiveConsoleReporter:
    def __init__(
        self,
        console: Console,
        *,
        json_output: bool = False,
        event_jsonl_path: Path | None = None,
    ) -> None:
        self.console = console
        self.json_output = json_output
        self.event_jsonl_path = event_jsonl_path

    async def on_event(self, event: EngineEvent) -> None:
        if self.event_jsonl_path is not None:
            self.event_jsonl_path.parent.mkdir(parents=True, exist_ok=True)
            with self.event_jsonl_path.open("a", encoding="utf-8") as handle:
                handle.write(json.dumps(event.to_dict()) + "\n")

        if self.json_output:
            self.console.print(json.dumps(event.to_dict()))
            return

        self.console.print(_render_event_text(event))

    async def finalize(self, result) -> None:
        return None


class ReporterFactory:
    def __init__(
        self,
        console: Console,
        json_output: bool,
        event_jsonl_path: Path | None,
    ) -> None:
        self.console = console
        self.json_output = json_output
        self.event_jsonl_path = event_jsonl_path

    def build(self) -> Reporter:
        return LiveConsoleReporter(
            self.console,
            json_output=self.json_output,
            event_jsonl_path=self.event_jsonl_path,
        )


def _render_event_text(event: EngineEvent) -> Text:
    kind = event.event_type
    payload = event.payload

    if kind == "suite.started":
        return Text(f"> Suite started: {payload.get('name')}")
    if kind == "suite.finished":
        return Text(
            "X Suite finished: "
            f"status={payload.get('status')} "
            f"passed={payload.get('passedCount', 0)} "
            f"failed={payload.get('failedCount', 0)} "
            f"skipped={payload.get('skippedCount', 0)}"
        )
    if kind == "test.started":
        return Text(f"  > Test started: {payload.get('id')}")
    if kind == "test.finished":
        return Text(f"  X Test finished: {payload.get('id')} status={payload.get('status')}")
    if kind == "step.started":
        return Text(f"    > Step started: {payload.get('id')}")
    if kind == "request.prepared":
        return Text(f"      -> Request: {payload.get('method')} {payload.get('url')}")
    if kind == "response.received":
        return Text(
            f"      <- Response: {payload.get('statusCode')} "
            f"in {round(float(payload.get('totalMs') or 0), 2)} ms"
        )
    if kind == "assertions.evaluated":
        return Text(
            f"      OK Assertions: passed={payload.get('passed')} count={payload.get('count')}"
        )
    if kind == "extraction.completed":
        names = ", ".join(payload.get("names") or [])
        return Text(f"      => Extraction: passed={payload.get('passed')} names=[{names}]")
    if kind == "artifact.saved":
        return Text(f"      [saved] Artifact: {payload.get('path')}")
    if kind == "validation.error":
        return Text(f"  ! Validation error [{payload.get('code')}]: {payload.get('message')}")
    if kind == "runtime.error":
        return Text(f"  ! Runtime error: {payload.get('message')}")
    if kind == "step.finished":
        return Text(f"    X Step finished: {payload.get('id')} status={payload.get('status')}")

    return Text(json.dumps(event.to_dict()))
