from __future__ import annotations

from pathlib import Path
from typing import Any

from ...diagnostics import Diagnostic, DiagnosticSeverity
from ...diagnostics_mapping import lookup_location
from ...models.assertions import (
    AssertionClause,
    BinaryExpectation,
    ExpectationSpec,
    HeaderExpectation,
    PerformanceExpectation,
    TextExpectation,
)
from ...models.common import SourceRef, SpecKind, SpecMetadata, VariableScope
from ...models.environment import EnvironmentSpec
from ...models.request import InputDefinition, OutputDefinition, PathParamEncoding, RequestDefinitionSpec, RequestSpec
from ...models.suite import (
    ArtifactSpec,
    ExtractionSpec,
    GlobalsSpec,
    LogSideSpec,
    LogSpec,
    RepeatSpec,
    RetryCondition,
    RetrySpec,
    StepSpec,
    SuiteInfo,
    TestSpec,
    TestSuiteSpec,
    WaitSpec,
)
from .schema import allowed_fields_for_kind


class V1SpecHandler:
    major_version = 1

    def supports_format(self, format_version: str) -> bool:
        return str(format_version).startswith("1.")

    def parse_document(self, path: Path, raw: dict[str, Any], source_map: dict[str, Any]) -> Any:
        kind = SpecKind(raw["kind"])
        metadata = SpecMetadata(
            format_version=str(raw["formatVersion"]),
            kind=kind,
            id=raw.get("id"),
            name=raw.get("name") or raw.get("info", {}).get("name"),
            description=raw.get("description") or raw.get("info", {}).get("description"),
            source=SourceRef(file=path, document_path="$", line=1, column=1),
        )
        if kind == SpecKind.REQUEST_DEFINITION:
            return self._parse_request_definition(path, raw, metadata)
        if kind == SpecKind.TEST_SUITE:
            return self._parse_suite(path, raw, metadata)
        if kind == SpecKind.ENVIRONMENT:
            return self._parse_environment(path, raw, metadata)
        raise ValueError(f"Unsupported kind {kind}")

    def validate_document(self, document: Any, raw: dict[str, Any], file: Path, source_map: dict[str, Any]) -> list[Diagnostic]:
        kind = raw.get("kind")
        diagnostics: list[Diagnostic] = []
        allowed = allowed_fields_for_kind(kind)
        if allowed:
            unknown = set(raw.keys()) - allowed
            for name in sorted(unknown):
                diagnostics.append(Diagnostic(
                    code="unknown-field",
                    message=f"Unknown field '{name}' for kind '{kind}'",
                    severity=DiagnosticSeverity.ERROR,
                    location=lookup_location(source_map, file, f"$.{name}"),
                ))
        return diagnostics

    def _parse_request_definition(self, path: Path, raw: dict[str, Any], metadata: SpecMetadata) -> RequestDefinitionSpec:
        request = self._parse_request(raw["request"])
        inputs = {
            name: InputDefinition(
                type=value.get("type"),
                required=bool(value.get("required", False)),
                sensitive=bool(value.get("sensitive", False)),
                description=value.get("description"),
            )
            for name, value in raw.get("inputs", {}).items()
        }
        outputs = {
            name: OutputDefinition(
                path=value["path"],
                required=bool(value.get("required", False)),
                sensitive=bool(value.get("sensitive", False)),
            )
            for name, value in raw.get("outputs", {}).items()
        }
        return RequestDefinitionSpec(metadata=metadata, request=request, inputs=inputs, outputs=outputs, raw=raw)

    def _parse_environment(self, path: Path, raw: dict[str, Any], metadata: SpecMetadata) -> EnvironmentSpec:
        return EnvironmentSpec(metadata=metadata, name=raw.get("name"), variables=raw.get("variables", {}), raw=raw)

    def _parse_suite(self, path: Path, raw: dict[str, Any], metadata: SpecMetadata) -> TestSuiteSpec:
        info = SuiteInfo(**raw.get("info", {}))
        globals_spec = GlobalsSpec(variables=raw.get("globals", {}).get("variables", {}))
        tests: list[TestSpec] = []
        for test in raw.get("tests", []):
            steps: list[StepSpec] = []
            for step in test.get("steps", []):
                step_spec = StepSpec(
                    id=step.get("id"),
                    name=step.get("name"),
                    skip=bool(step.get("skip", False)),
                    continue_on_failure=bool(step.get("continueOnFailure", False)),
                    variables=step.get("variables", {}),
                    wait=self._parse_wait(step.get("wait")),
                    use=step.get("use"),
                    with_=step.get("with", {}),
                    request=self._parse_request(step["request"]) if "request" in step else None,
                    extend=step.get("extend", {}),
                    overrides=step.get("overrides", {}),
                    timeout_ms=step.get("timeoutMs"),
                    expect=self._parse_expect(step.get("expect")),
                    extract=self._parse_extract(step.get("extract", {})),
                    retry=self._parse_retry(step.get("retry")),
                    repeat=self._parse_repeat(step.get("repeat")),
                    log=self._parse_log(step.get("log")),
                    artifacts=self._parse_artifacts(step.get("artifacts")),
                )
                steps.append(step_spec)
            tests.append(TestSpec(
                id=test["id"],
                name=test.get("name"),
                tags=test.get("tags", []),
                skip=bool(test.get("skip", False)),
                skip_reason=test.get("skipReason"),
                variables=test.get("variables", {}),
                steps=steps,
            ))
        return TestSuiteSpec(metadata=metadata, info=info, globals=globals_spec, tests=tests, raw=raw)

    def _parse_wait(self, raw: dict[str, Any] | None) -> WaitSpec | None:
        if raw is None:
            return None
        return WaitSpec(before_ms=raw.get("beforeMs"), after_ms=raw.get("afterMs"), for_ms=raw.get("forMs"))

    def _parse_retry(self, raw: dict[str, Any] | None) -> RetrySpec | None:
        if raw is None:
            return None
        return RetrySpec(count=int(raw.get("count", 0)), delay_ms=int(raw.get("delayMs", 0)), when=RetryCondition(status_in=raw.get("when", {}).get("statusIn", [])))

    def _parse_repeat(self, raw: dict[str, Any] | None) -> RepeatSpec | None:
        if raw is None:
            return None
        return RepeatSpec(warmup_count=int(raw.get("warmupCount", 0)), count=int(raw.get("count", 1)))

    def _parse_log(self, raw: dict[str, Any] | None) -> LogSpec | None:
        if raw is None:
            return None
        return LogSpec(request=LogSideSpec(**raw.get("request", {})), response=LogSideSpec(**raw.get("response", {})))

    def _parse_artifacts(self, raw: dict[str, Any] | None) -> ArtifactSpec | None:
        if raw is None:
            return None
        return ArtifactSpec(
            save_response_body_to=raw.get("saveResponseBodyTo"),
            save_parsed_json_to=raw.get("saveParsedJsonTo"),
            save_headers_to=raw.get("saveHeadersTo"),
            save_timing_to=raw.get("saveTimingTo"),
        )

    def _parse_extract(self, raw: dict[str, Any]) -> dict[str, ExtractionSpec]:
        return {
            name: ExtractionSpec(
                from_selector=value.get("from"),
                from_definition=value.get("fromDefinition"),
                scope=VariableScope(value.get("scope", "test")),
                required=bool(value.get("required", False)),
                sensitive=bool(value.get("sensitive", False)),
            )
            for name, value in raw.items()
        }

    def _parse_expect(self, raw: dict[str, Any] | None) -> ExpectationSpec | None:
        if raw is None:
            return None
        headers = {name: HeaderExpectation(operators=value) for name, value in raw.get("headers", {}).items()}
        body = self._parse_clause(raw.get("body")) if raw.get("body") else None
        text = TextExpectation(operators=raw.get("text", {})) if raw.get("text") else None
        binary = BinaryExpectation(operators=raw.get("binary", {})) if raw.get("binary") else None
        perf = PerformanceExpectation(metrics=raw.get("performance", {})) if raw.get("performance") else None
        return ExpectationSpec(status=raw.get("status"), body=body, headers=headers, text=text, binary=binary, performance=perf)

    def _parse_clause(self, raw: dict[str, Any]) -> AssertionClause:
        and_clauses = [self._parse_clause(x) for x in raw.get("and", [])]
        or_clauses = [self._parse_clause(x) for x in raw.get("or", [])]
        operators = {k: v for k, v in raw.items() if k not in {"path", "field", "and", "or"}}
        return AssertionClause(path=raw.get("path"), element_field=raw.get("field"), operators=operators, and_=and_clauses, or_=or_clauses)

    def _parse_request(self, raw: dict[str, Any]) -> RequestSpec:
        return RequestSpec(
            method=raw["method"],
            url=raw.get("url"),
            base_url=raw.get("baseUrl"),
            path=raw.get("path"),
            path_params=raw.get("pathParams", {}),
            path_param_encoding=PathParamEncoding(enabled=bool(raw.get("pathParamEncoding", {}).get("enabled", False))),
            query=raw.get("query", {}),
            headers=raw.get("headers", {}),
            body=raw.get("body"),
            body_raw=raw.get("bodyRaw"),
            body_file=raw.get("bodyFile"),
            body_file_mode=raw.get("bodyFileMode"),
            form=raw.get("form", {}),
            multipart=raw.get("multipart", {}),
            timeout_ms=raw.get("timeoutMs"),
            follow_redirects=raw.get("followRedirects"),
        )
