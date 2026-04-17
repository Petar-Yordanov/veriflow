from __future__ import annotations

import asyncio
from pathlib import Path

import typer

from ..cli_context import cli_context
from ..exit_codes import ExitCode
from ..integration.engine_gateway import EngineGateway
from ..integration.run_plan import (
    SelectionOptions,
    SuiteRunInput,
    build_discovered_run_inputs,
    build_single_suite_run_input,
)
from ..rendering.summary import render_suite_summary

app = typer.Typer(no_args_is_help=True, help="Run suites through the shared engine.")


@app.command("suite")
def run_suite(
    suite: Path = typer.Argument(..., exists=True, dir_okay=False, readable=True),
    environment: str | None = typer.Option(None, "--environment", "-e", help="Environment path or discovered environment name."),
    project_root: Path | None = typer.Option(None, "--project-root", file_okay=False),
    var_file: list[Path] | None = typer.Option(None, "--var-file", exists=True, dir_okay=False, readable=True),
    var: list[str] | None = typer.Option(None, "--var", help="Ad-hoc variable in key=value form. May be repeated."),
    test_id: list[str] | None = typer.Option(None, "--test-id", help="Only include tests with these ids."),
    test_name: list[str] | None = typer.Option(None, "--test-name", help="Only include tests with these names."),
    tag: list[str] | None = typer.Option(None, "--tag", help="Only include tests containing any of these tags."),
    report_json: Path | None = typer.Option(None, "--report-json", help="Write final suite result as JSON."),
    event_jsonl: Path | None = typer.Option(None, "--event-jsonl", help="Write streamed execution events as JSONL."),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable JSON events and summary to stdout."),
) -> None:
    selection = SelectionOptions(
        test_ids=set(test_id or []),
        test_names=set(test_name or []),
        tags=set(tag or []),
    )
    run_input = build_single_suite_run_input(
        suite_path=suite,
        project_root=project_root,
        environment_selector=environment,
        variable_files=list(var_file or []),
        ad_hoc_vars=list(var or []),
        selection=selection,
    )
    _run_many([run_input], report_json=report_json, event_jsonl=event_jsonl, json_output=json_output)


@app.command("discovered")
def run_discovered(
    project_root: Path = typer.Argument(..., exists=True, file_okay=False, readable=True),
    environment: str | None = typer.Option(None, "--environment", "-e", help="Environment path or discovered environment name."),
    suite_id: list[str] | None = typer.Option(None, "--suite-id", help="Run only suites whose info.id or top-level id matches."),
    suite_name: list[str] | None = typer.Option(None, "--suite-name", help="Run only suites whose name matches."),
    test_id: list[str] | None = typer.Option(None, "--test-id", help="Only include tests with these ids."),
    test_name: list[str] | None = typer.Option(None, "--test-name", help="Only include tests with these names."),
    tag: list[str] | None = typer.Option(None, "--tag", help="Only include tests containing any of these tags."),
    var_file: list[Path] | None = typer.Option(None, "--var-file", exists=True, dir_okay=False, readable=True),
    var: list[str] | None = typer.Option(None, "--var", help="Ad-hoc variable in key=value form. May be repeated."),
    report_dir: Path | None = typer.Option(None, "--report-dir", help="Write one JSON report per suite into this directory."),
    event_jsonl: Path | None = typer.Option(None, "--event-jsonl", help="Append streamed execution events as JSONL."),
    json_output: bool = typer.Option(False, "--json", help="Emit machine-readable JSON events and summary to stdout."),
) -> None:
    selection = SelectionOptions(
        test_ids=set(test_id or []),
        test_names=set(test_name or []),
        tags=set(tag or []),
    )
    run_inputs = build_discovered_run_inputs(
        project_root=project_root,
        environment_selector=environment,
        variable_files=list(var_file or []),
        ad_hoc_vars=list(var or []),
        suite_ids=set(suite_id or []),
        suite_names=set(suite_name or []),
        selection=selection,
    )
    _run_many(run_inputs, report_dir=report_dir, event_jsonl=event_jsonl, json_output=json_output)


def _run_many(
    run_inputs: list[SuiteRunInput],
    *,
    report_json: Path | None = None,
    report_dir: Path | None = None,
    event_jsonl: Path | None = None,
    json_output: bool = False,
) -> None:
    gateway = EngineGateway()
    reporter_factory = cli_context.reporter_factory(
        json_output=json_output,
        event_jsonl_path=event_jsonl,
    )

    overall_failed = False
    for item in run_inputs:
        if not json_output:
            cli_context.console.rule(f"Run {item.display_name}")
        result = asyncio.run(
            gateway.run_suite(
                suite_path=item.suite_path,
                environment_path=item.environment_path,
                reporter=reporter_factory.build(),
                report_json_path=(report_json if report_json else _suite_report_path(report_dir, item)),
            )
        )
        render_suite_summary(result, json_output=json_output)
        overall_failed = overall_failed or (result.status.value == "failed")
    if overall_failed:
        raise typer.Exit(ExitCode.RUNTIME_FAILED)


def _suite_report_path(report_dir: Path | None, item: SuiteRunInput) -> Path | None:
    if report_dir is None:
        return None
    report_dir.mkdir(parents=True, exist_ok=True)
    return report_dir / f"{item.display_name.replace('/', '_').replace(' ', '_')}.json"
