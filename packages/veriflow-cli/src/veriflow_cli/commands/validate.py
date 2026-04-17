from __future__ import annotations

from pathlib import Path

import typer

from ..cli_context import cli_context
from ..exit_codes import ExitCode
from ..integration.engine_gateway import EngineGateway
from ..rendering.diagnostics import render_diagnostics

app = typer.Typer(no_args_is_help=True, help="Validate files and projects.")


@app.command("file")
def validate_file(
    path: Path = typer.Argument(..., exists=True, dir_okay=False, readable=True),
    environment: Path | None = typer.Option(None, "--environment", "-e", exists=True, dir_okay=False),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable diagnostics."),
) -> None:
    gateway = EngineGateway()
    result = gateway.validate_suite(path, environment_path=environment)
    render_diagnostics(result.diagnostics, as_json=json_output)
    if not result.ok:
        raise typer.Exit(ExitCode.VALIDATION_FAILED)


@app.command("project")
def validate_project(
    project_root: Path = typer.Argument(..., exists=True, file_okay=False, readable=True),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable diagnostics."),
) -> None:
    gateway = EngineGateway()
    discovery = gateway.discover(project_root)
    any_failed = False
    for suite_path in discovery.suites:
        result = gateway.validate_suite(suite_path)
        cli_context.console.rule(f"Validate {suite_path.relative_to(project_root)}")
        render_diagnostics(result.diagnostics, as_json=json_output)
        any_failed = any_failed or (not result.ok)
    if any_failed:
        raise typer.Exit(ExitCode.VALIDATION_FAILED)
