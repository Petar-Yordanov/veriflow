from __future__ import annotations

import json
from dataclasses import asdict, is_dataclass

from rich.table import Table

from ..cli_context import cli_context


def render_diagnostics(diagnostics, *, as_json: bool = False) -> None:
    if as_json:
        payload = [_diag_to_dict(d) for d in diagnostics]
        cli_context.console.print_json(json.dumps(payload))
        return

    if not diagnostics:
        cli_context.console.print("No diagnostics.")
        return

    table = Table(title="Diagnostics")
    table.add_column("Severity")
    table.add_column("Code")
    table.add_column("Message")
    table.add_column("File")
    table.add_column("Path")
    table.add_column("Line:Col")

    for diag in diagnostics:
        location = getattr(diag, "location", None)
        file = str(location.file) if location and getattr(location, "file", None) else ""
        path = location.document_path if location else ""
        line = getattr(location, "line", None)
        col = getattr(location, "column", None)
        pos = f"{line or ''}:{col or ''}" if (line or col) else ""
        table.add_row(
            str(diag.severity.value),
            str(diag.code),
            str(diag.message),
            file,
            path or "",
            pos,
        )

    cli_context.console.print(table)


def _diag_to_dict(diag):
    if is_dataclass(diag):
        return asdict(diag)
    return dict(diag)
