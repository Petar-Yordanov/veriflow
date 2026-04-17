from __future__ import annotations

import asyncio
import json
import statistics
import time
from dataclasses import dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Awaitable, Callable

from ..artifacts.persistence import ArtifactManager
from ..diagnostics import DiagnosticSeverity
from ..events.base import EngineEvent
from ..events import types as ev
from ..models.common import ResultStatus, TimingMetrics
from ..models.documents import LoadedBundle
from ..models.request import RequestDefinitionSpec, RequestSpec
from ..models.results import RequestSummary, ResponseSummary, StepResult, SuiteResult, TestResult
from ..reporting.base import Reporter
from ..runtime.context import RunContextFactory
from ..runtime.interpolation import Interpolator
from ..runtime.redaction import Redactor
from ..validation.validator import ValidationResult
from .assertions import AssertionEngine
from .extraction import ExtractionEngine
from .http import HttpExecutor, RequestPreparer


EventSink = Callable[[EngineEvent], Awaitable[None]]


@dataclass(slots=True)
class RunnerOptions:
    seed: int = 42
    project_root: Path | None = None
    artifacts_root: Path | None = None
    stop_on_validation_error: bool = True


@dataclass(slots=True)
class RunnerDependencies:
    artifact_manager: ArtifactManager
    run_context_factory: RunContextFactory
    http_executor: HttpExecutor = field(default_factory=HttpExecutor)
    request_preparer: RequestPreparer = field(default_factory=RequestPreparer)
    interpolator: Interpolator = field(default_factory=Interpolator)
    assertion_engine: AssertionEngine = field(default_factory=AssertionEngine)
    extraction_engine: ExtractionEngine = field(default_factory=ExtractionEngine)
    redactor: Redactor = field(default_factory=Redactor)


class EngineRunner:
    def __init__(self, options: RunnerOptions, dependencies: RunnerDependencies) -> None:
        self.options = options
        self.deps = dependencies

    async def run(self, bundle: LoadedBundle, validation: ValidationResult, *, reporter: Reporter, event_sink: EventSink | None = None) -> SuiteResult:
        async def emit(event: EngineEvent) -> None:
            await reporter.on_event(event)
            if event_sink is not None:
                await event_sink(event)

        for diag in validation.diagnostics:
            if diag.severity == DiagnosticSeverity.ERROR:
                await emit(ev.validation_error(code=diag.code, message=diag.message, file=str(diag.location.file) if diag.location.file else None, path=diag.location.document_path, line=diag.location.line, column=diag.location.column))
        if self.options.stop_on_validation_error and not validation.ok:
            now = datetime.now(UTC).isoformat()
            result = SuiteResult(status=ResultStatus.FAILED, started_at=now, finished_at=now, duration_ms=0.0, passed_count=0, failed_count=0, skipped_count=0, tests=[], diagnostics=[self._diagnostic_to_dict(d) for d in validation.diagnostics])
            await reporter.finalize(result)
            return result

        suite = bundle.suite.typed
        started = time.perf_counter()
        started_at = datetime.now(UTC).isoformat()
        suite_runtime: dict[str, Any] = {}
        test_results: list[TestResult] = []
        await emit(ev.suite_started(name=suite.info.name, file=str(bundle.suite.path)))

        for test in suite.tests:
            test_started_perf = time.perf_counter()
            test_started_at = datetime.now(UTC).isoformat()
            await emit(ev.test_started(id=test.id, name=test.name))
            if test.skip:
                test_result = TestResult(id=test.id, name=test.name, status=ResultStatus.SKIPPED, started_at=test_started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=0.0, tags=test.tags, steps=[])
                test_results.append(test_result)
                await emit(ev.test_finished(id=test.id, name=test.name, status=test_result.status.value))
                continue

            test_runtime: dict[str, Any] = {}
            step_results: list[StepResult] = []
            test_status = ResultStatus.PASSED
            for step in test.steps:
                step_result = await self._run_step(bundle, suite_runtime, test_runtime, test, step, emit)
                step_results.append(step_result)
                if step_result.status == ResultStatus.FAILED and not step.continue_on_failure:
                    test_status = ResultStatus.FAILED
                    break
                if step_result.status == ResultStatus.FAILED:
                    test_status = ResultStatus.FAILED
            if all(step.status == ResultStatus.SKIPPED for step in step_results) and step_results:
                test_status = ResultStatus.SKIPPED
            test_result = TestResult(id=test.id, name=test.name, status=test_status, started_at=test_started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=(time.perf_counter() - test_started_perf) * 1000, tags=test.tags, steps=step_results)
            test_results.append(test_result)
            await emit(ev.test_finished(id=test.id, name=test.name, status=test_status.value))

        passed_count = sum(1 for t in test_results if t.status == ResultStatus.PASSED)
        failed_count = sum(1 for t in test_results if t.status == ResultStatus.FAILED)
        skipped_count = sum(1 for t in test_results if t.status == ResultStatus.SKIPPED)
        suite_status = ResultStatus.FAILED if failed_count else ResultStatus.PASSED
        if test_results and all(t.status == ResultStatus.SKIPPED for t in test_results):
            suite_status = ResultStatus.SKIPPED
        result = SuiteResult(status=suite_status, started_at=started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=(time.perf_counter() - started) * 1000, passed_count=passed_count, failed_count=failed_count, skipped_count=skipped_count, tests=test_results, diagnostics=[self._diagnostic_to_dict(d) for d in validation.diagnostics])
        await emit(ev.suite_finished(status=suite_status.value, passedCount=passed_count, failedCount=failed_count, skippedCount=skipped_count))
        await reporter.finalize(result)
        return result

    async def _run_step(self, bundle: LoadedBundle, suite_runtime: dict[str, Any], test_runtime: dict[str, Any], test, step, emit: EventSink) -> StepResult:
        step_started_perf = time.perf_counter()
        step_started_at = datetime.now(UTC).isoformat()
        await emit(ev.step_started(id=step.id, name=step.name))
        if step.skip:
            result = StepResult(id=step.id, name=step.name, status=ResultStatus.SKIPPED, started_at=step_started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=0.0)
            await emit(ev.step_finished(id=step.id, name=step.name, status=result.status.value))
            return result
        try:
            if step.wait and step.wait.before_ms:
                await asyncio.sleep(step.wait.before_ms / 1000)
            if step.wait and step.wait.for_ms and not step.use and not step.request:
                await asyncio.sleep(step.wait.for_ms / 1000)
                result = StepResult(id=step.id, name=step.name, status=ResultStatus.PASSED, started_at=step_started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=(time.perf_counter() - step_started_perf) * 1000)
                await emit(ev.step_finished(id=step.id, name=step.name, status=result.status.value))
                return result

            request_definition = self._resolve_request_definition(bundle, step)
            request_spec = request_definition.request if request_definition else step.request
            request_spec = self._apply_mutations(request_spec, step)

            inputs = dict(step.with_)
            layers = self.deps.run_context_factory.create_layers(
                seed=self.options.seed,
                environment=bundle.environment.typed.variables if bundle.environment else {},
                suite_declared=bundle.suite.typed.globals.variables,
                suite_runtime=suite_runtime,
                test_declared=test.variables,
                test_runtime=test_runtime,
                step_declared=step.variables,
                inputs=inputs,
                builtin_context={
                    "suite_id": bundle.suite.typed.metadata.id,
                    "suite_name": bundle.suite.typed.info.name,
                    "test_id": test.id,
                    "test_name": test.name,
                    "step_id": step.id,
                    "step_name": step.name,
                    "environment_name": bundle.environment.typed.name if bundle.environment else None,
                    "iteration_index": 0,
                },
            )
            lookup = layers.as_lookup()
            prepared = self.deps.request_preparer.prepare(request_spec, lookup, project_root=self.options.project_root)
            await emit(ev.request_prepared(stepId=step.id, method=prepared.method, url=prepared.url))

            timings: list[float] = []
            response = None
            attempts = 0
            max_attempts = 1 + (step.retry.count if step.retry else 0)
            while attempts < max_attempts:
                attempts += 1
                call_started = time.perf_counter()
                response = await self.deps.http_executor.send(prepared, timeout_ms=step.timeout_ms or request_spec.timeout_ms, follow_redirects=request_spec.follow_redirects)
                total_ms = (time.perf_counter() - call_started) * 1000
                timings.append(total_ms)
                retryable = step.retry and response.status_code in step.retry.when.status_in and attempts < max_attempts
                if retryable:
                    await asyncio.sleep(step.retry.delay_ms / 1000)
                    continue
                break
            assert response is not None
            timing_data = self._timing_summary(timings)
            await emit(ev.response_received(stepId=step.id, statusCode=response.status_code, totalMs=timing_data["totalMs"]))
            json_body = None
            try:
                json_body = response.json()
            except Exception:
                json_body = None
            assertion_outcome = self.deps.assertion_engine.evaluate(step.expect, response, timing_data, json_body)
            await emit(ev.assertions_evaluated(stepId=step.id, passed=assertion_outcome.passed, count=len(assertion_outcome.evaluations)))
            extracted, extraction_results, extraction_ok = self.deps.extraction_engine.extract(step.extract, request_definition, json_body)
            await emit(ev.extraction_completed(stepId=step.id, names=list(extracted.keys()), passed=extraction_ok))
            for name, value in extracted.items():
                scope = step.extract[name].scope.value
                if scope == "suite":
                    suite_runtime[name] = value
                elif scope == "test":
                    test_runtime[name] = value
            artifacts = await self._persist_artifacts(step, response, json_body, timing_data, emit)
            if step.wait and step.wait.after_ms:
                await asyncio.sleep(step.wait.after_ms / 1000)
            passed = assertion_outcome.passed and extraction_ok
            status = ResultStatus.PASSED if passed else ResultStatus.FAILED
            result = StepResult(
                id=step.id,
                name=step.name,
                status=status,
                started_at=step_started_at,
                finished_at=datetime.now(UTC).isoformat(),
                duration_ms=(time.perf_counter() - step_started_perf) * 1000,
                request_summary=RequestSummary(method=prepared.method, url=prepared.url, headers=prepared.headers),
                response_summary=ResponseSummary(status_code=response.status_code, headers=dict(response.headers), body_preview=response.text[:500]),
                assertions=assertion_outcome.evaluations,
                extractions=extraction_results,
                timing=TimingMetrics(total_ms=timing_data.get("totalMs"), avg_ms=timing_data.get("avgMs"), p95_ms=timing_data.get("p95Ms"), max_ms=timing_data.get("maxMs")),
                artifacts=artifacts,
            )
            await emit(ev.step_finished(id=step.id, name=step.name, status=result.status.value))
            return result
        except Exception as exc:
            await emit(ev.runtime_error(stepId=step.id, message=str(exc)))
            result = StepResult(id=step.id, name=step.name, status=ResultStatus.FAILED, started_at=step_started_at, finished_at=datetime.now(UTC).isoformat(), duration_ms=(time.perf_counter() - step_started_perf) * 1000, error=str(exc))
            await emit(ev.step_finished(id=step.id, name=step.name, status=result.status.value))
            return result

    def _resolve_request_definition(self, bundle: LoadedBundle, step) -> RequestDefinitionSpec | None:
        if not step.use:
            return None
        ref_path = Path(step.use)
        if not ref_path.is_absolute():
            ref_path = (bundle.suite.path.parent / ref_path).resolve()
        return bundle.referenced_requests[ref_path].typed

    def _apply_mutations(self, request_spec: RequestSpec, step) -> RequestSpec:
        if not step.extend and not step.overrides and not step.timeout_ms:
            return request_spec
        raw = {
            "method": request_spec.method,
            "url": request_spec.url,
            "baseUrl": request_spec.base_url,
            "path": request_spec.path,
            "pathParams": dict(request_spec.path_params),
            "query": dict(request_spec.query),
            "headers": dict(request_spec.headers),
            "body": request_spec.body,
            "bodyRaw": request_spec.body_raw,
            "timeoutMs": step.timeout_ms or request_spec.timeout_ms,
            "followRedirects": request_spec.follow_redirects,
        }
        raw = self._deep_merge(raw, step.extend)
        raw = self._deep_replace(raw, step.overrides)
        return RequestSpec(
            method=raw["method"],
            url=raw.get("url"),
            base_url=raw.get("baseUrl"),
            path=raw.get("path"),
            path_params=raw.get("pathParams", {}),
            query=raw.get("query", {}),
            headers=raw.get("headers", {}),
            body=raw.get("body"),
            body_raw=raw.get("bodyRaw"),
            timeout_ms=raw.get("timeoutMs"),
            follow_redirects=raw.get("followRedirects"),
        )

    def _deep_merge(self, base: dict[str, Any], patch: dict[str, Any]) -> dict[str, Any]:
        out = dict(base)
        for key, value in patch.items():
            if key in out and isinstance(out[key], dict) and isinstance(value, dict):
                out[key] = self._deep_merge(out[key], value)
            else:
                out[key] = value
        return out

    def _deep_replace(self, base: dict[str, Any], patch: dict[str, Any]) -> dict[str, Any]:
        out = dict(base)
        for key, value in patch.items():
            out[key] = value
        return out

    async def _persist_artifacts(self, step, response, json_body, timing_data, emit: EventSink) -> list[str]:
        paths: list[str] = []
        if not step.artifacts:
            return paths
        root = self.options.artifacts_root or Path.cwd() / "artifacts"
        if step.artifacts.save_response_body_to:
            p = self.deps.artifact_manager.save_text(root / step.artifacts.save_response_body_to, response.text)
            paths.append(str(p))
            await emit(ev.artifact_saved(stepId=step.id, path=str(p), kind="responseBody"))
        if step.artifacts.save_parsed_json_to and json_body is not None:
            p = self.deps.artifact_manager.save_json(root / step.artifacts.save_parsed_json_to, json_body)
            paths.append(str(p))
            await emit(ev.artifact_saved(stepId=step.id, path=str(p), kind="parsedJson"))
        if step.artifacts.save_headers_to:
            p = self.deps.artifact_manager.save_json(root / step.artifacts.save_headers_to, dict(response.headers))
            paths.append(str(p))
            await emit(ev.artifact_saved(stepId=step.id, path=str(p), kind="headers"))
        if step.artifacts.save_timing_to:
            p = self.deps.artifact_manager.save_json(root / step.artifacts.save_timing_to, timing_data)
            paths.append(str(p))
            await emit(ev.artifact_saved(stepId=step.id, path=str(p), kind="timing"))
        return paths

    def _timing_summary(self, timings: list[float]) -> dict[str, float]:
        sorted_timings = sorted(timings)
        index = max(0, min(len(sorted_timings) - 1, round(0.95 * (len(sorted_timings) - 1))))
        return {
            "totalMs": sorted_timings[-1],
            "avgMs": statistics.fmean(sorted_timings),
            "p95Ms": sorted_timings[index],
            "maxMs": max(sorted_timings),
        }

    def _diagnostic_to_dict(self, d) -> dict[str, Any]:
        return {
            "code": d.code,
            "message": d.message,
            "severity": d.severity.value,
            "file": str(d.location.file) if d.location.file else None,
            "path": d.location.document_path,
            "line": d.location.line,
            "column": d.location.column,
        }
