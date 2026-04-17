from __future__ import annotations

import json
from pathlib import Path

import typer
from rich.table import Table

from ..cli_context import cli_context
from ..integration.engine_gateway import EngineGateway

app = typer.Typer(no_args_is_help=True, help="Discover suites, requests, and environments by convention.")


def _render_paths(title: str, project_root: Path, items: list[Path], *, as_json: bool) -> None:
    rendered_items: list[str] = []
    for item in items:
        try:
            display = str(item.relative_to(project_root))
        except ValueError:
            display = str(item)
        rendered_items.append(display)

    if as_json:
        cli_context.console.print_json(
            json.dumps(
                {
                    "projectRoot": str(project_root),
                    "title": title,
                    "paths": rendered_items,
                }
            )
        )
        return

    table = Table(title=title)
    table.add_column("Path")
    for item in rendered_items:
        table.add_row(item)
    cli_context.console.print(table)


@app.command("suites")
def discover_suites(
    project_root: Path = typer.Argument(..., exists=True, file_okay=False, readable=True),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable JSON."),
) -> None:
    discovery = EngineGateway().discover(project_root)
    _render_paths("Suites", project_root, discovery.suites, as_json=json_output)


@app.command("requests")
def discover_requests(
    project_root: Path = typer.Argument(..., exists=True, file_okay=False, readable=True),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable JSON."),
) -> None:
    discovery = EngineGateway().discover(project_root)
    _render_paths("Request Definitions", project_root, discovery.requests, as_json=json_output)


@app.command("environments")
def discover_environments(
    project_root: Path = typer.Argument(..., exists=True, file_okay=False, readable=True),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable JSON."),
) -> None:
    discovery = EngineGateway().discover(project_root)
    _render_paths("Environments", project_root, discovery.environments, as_json=json_output)
