from __future__ import annotations

from dataclasses import dataclass, field

from rich.console import Console

from .rendering.events import ReporterFactory


@dataclass(slots=True)
class CliContext:
    console: Console = field(default_factory=Console)

    def reporter_factory(self, *, json_output: bool, event_jsonl_path):
        return ReporterFactory(
            console=self.console,
            json_output=json_output,
            event_jsonl_path=event_jsonl_path,
        )


cli_context = CliContext()
