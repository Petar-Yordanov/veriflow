from __future__ import annotations

import json
from dataclasses import asdict

from rich.table import Table

from ..cli_context import cli_context


def render_suite_summary(result, *, json_output: bool = False) -> None:
    if json_output:
        cli_context.console.print(json.dumps(asdict(result)))
        return

    table = Table(title="Suite Summary")
    table.add_column("Status")
    table.add_column("Passed")
    table.add_column("Failed")
    table.add_column("Skipped")
    table.add_column("Duration (ms)")
    table.add_row(
        result.status.value,
        str(result.passed_count),
        str(result.failed_count),
        str(result.skipped_count),
        f"{result.duration_ms:.2f}",
    )
    cli_context.console.print(table)

    details = Table(title="Tests")
    details.add_column("Id")
    details.add_column("Name")
    details.add_column("Status")
    details.add_column("Duration (ms)")
    details.add_column("Tags")
    for test in result.tests:
        details.add_row(
            test.id,
            test.name or "",
            test.status.value,
            f"{test.duration_ms:.2f}",
            ", ".join(test.tags),
        )
    cli_context.console.print(details)
