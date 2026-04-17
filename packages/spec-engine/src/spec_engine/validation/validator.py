from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

from ..diagnostics import Diagnostic, DiagnosticSeverity
from ..diagnostics_mapping import lookup_location
from ..models.documents import LoadedBundle, LoadedDocument
from ..models.environment import EnvironmentSpec
from ..models.request import RequestDefinitionSpec, RequestSpec
from ..models.suite import StepSpec, TestSuiteSpec
from ..versions.registry import VersionRegistry
from .selector import is_supported_jsonpath


@dataclass(slots=True)
class ValidationResult:
    diagnostics: list[Diagnostic] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not any(d.is_error for d in self.diagnostics)


class SpecValidator:
    def __init__(self, registry: VersionRegistry) -> None:
        self._registry = registry

    def validate_bundle(self, bundle: LoadedBundle) -> ValidationResult:
        diagnostics: list[Diagnostic] = []
        diagnostics.extend(self._validate_document(bundle.suite))
        if bundle.environment:
            diagnostics.extend(self._validate_document(bundle.environment))
        for document in bundle.referenced_requests.values():
            diagnostics.extend(self._validate_document(document))
        diagnostics.extend(self._validate_cross_file(bundle))
        return ValidationResult(diagnostics=diagnostics)

    def _validate_document(self, document: LoadedDocument) -> list[Diagnostic]:
        handler = self._registry.handler_for_document(document.raw)
        diagnostics = handler.validate_document(document.typed, document.raw, document.path, document.source_map)
        typed = document.typed
        if isinstance(typed, TestSuiteSpec):
            diagnostics.extend(self._validate_suite(document.path, typed, document.source_map))
        elif isinstance(typed, RequestDefinitionSpec):
            diagnostics.extend(self._validate_request_definition(document.path, typed, document.source_map))
        elif isinstance(typed, EnvironmentSpec):
            if not isinstance(typed.variables, dict):
                diagnostics.append(self._diag(document.path, document.source_map, "$.variables", "environment-variables", "Environment variables must be a mapping"))
        return diagnostics

    def _validate_suite(self, file: Path, suite: TestSuiteSpec, source_map: dict) -> list[Diagnostic]:
        diagnostics: list[Diagnostic] = []
        seen_test_ids: set[str] = set()
        for t_index, test in enumerate(suite.tests):
            if test.id in seen_test_ids:
                diagnostics.append(self._diag(file, source_map, f"$.tests[{t_index}].id", "duplicate-test-id", f"Duplicate test id '{test.id}'"))
            seen_test_ids.add(test.id)
            seen_step_ids: set[str] = set()
            for s_index, step in enumerate(test.steps):
                if step.id and step.id in seen_step_ids:
                    diagnostics.append(self._diag(file, source_map, f"$.tests[{t_index}].steps[{s_index}].id", "duplicate-step-id", f"Duplicate step id '{step.id}' in test '{test.id}'"))
                if step.id:
                    seen_step_ids.add(step.id)
                diagnostics.extend(self._validate_step(file, source_map, t_index, s_index, step))
        return diagnostics

    def _validate_step(self, file: Path, source_map: dict, t_index: int, s_index: int, step: StepSpec) -> list[Diagnostic]:
        diagnostics: list[Diagnostic] = []
        base = f"$.tests[{t_index}].steps[{s_index}]"
        if bool(step.use) == bool(step.request) and not step.wait:
            diagnostics.append(self._diag(file, source_map, base, "step-shape", "Step must define exactly one of 'use' or 'request', unless it is a wait-only step"))
        if step.request:
            diagnostics.extend(self._validate_request(file, source_map, f"{base}.request", step.request))
        if step.expect and step.expect.body:
            diagnostics.extend(self._validate_assertion_clause(file, source_map, f"{base}.expect.body", step.expect.body))
        for name, extraction in step.extract.items():
            if bool(extraction.from_selector) == bool(extraction.from_definition):
                diagnostics.append(self._diag(file, source_map, f"{base}.extract.{name}", "extract-source", f"Extraction '{name}' must define exactly one of 'from' or 'fromDefinition'"))
            if extraction.from_selector and not is_supported_jsonpath(extraction.from_selector):
                diagnostics.append(self._diag(file, source_map, f"{base}.extract.{name}.from", "invalid-selector", f"Unsupported JSONPath selector '{extraction.from_selector}'"))
        return diagnostics

    def _validate_assertion_clause(self, file: Path, source_map: dict, path: str, clause) -> list[Diagnostic]:
        diagnostics: list[Diagnostic] = []
        if clause.path and not is_supported_jsonpath(clause.path):
            diagnostics.append(self._diag(file, source_map, path, "invalid-selector", f"Unsupported JSONPath selector '{clause.path}'"))
        for idx, child in enumerate(clause.and_):
            diagnostics.extend(self._validate_assertion_clause(file, source_map, f"{path}.and[{idx}]", child))
        for idx, child in enumerate(clause.or_):
            diagnostics.extend(self._validate_assertion_clause(file, source_map, f"{path}.or[{idx}]", child))
        return diagnostics

    def _validate_request_definition(self, file: Path, request_def: RequestDefinitionSpec, source_map: dict) -> list[Diagnostic]:
        diagnostics = self._validate_request(file, source_map, "$.request", request_def.request)
        for name, output in request_def.outputs.items():
            if not is_supported_jsonpath(output.path):
                diagnostics.append(self._diag(file, source_map, f"$.outputs.{name}.path", "invalid-selector", f"Unsupported JSONPath selector '{output.path}'"))
        return diagnostics

    def _validate_request(self, file: Path, source_map: dict, base: str, request: RequestSpec) -> list[Diagnostic]:
        diagnostics: list[Diagnostic] = []
        body_modes = [request.body is not None, request.body_raw is not None, request.body_file is not None, bool(request.form), bool(request.multipart)]
        if sum(1 for item in body_modes if item) > 1:
            diagnostics.append(self._diag(file, source_map, base, "conflicting-body-modes", "Request defines conflicting body modes"))
        if not request.url and not (request.base_url and request.path):
            diagnostics.append(self._diag(file, source_map, base, "request-url-shape", "Request must define 'url' or both 'baseUrl' and 'path'"))
        for key, value in request.query.items():
            if value is None:
                diagnostics.append(self._diag(file, source_map, f"{base}.query.{key}", "null-query-value", f"Query parameter '{key}' cannot be null"))
        return diagnostics

    def _validate_cross_file(self, bundle: LoadedBundle) -> list[Diagnostic]:
        diagnostics: list[Diagnostic] = []
        suite = bundle.suite.typed
        for test in suite.tests:
            for step in test.steps:
                if step.use:
                    ref_path = Path(step.use)
                    if not ref_path.is_absolute():
                        ref_path = (bundle.suite.path.parent / ref_path).resolve()
                    document = bundle.referenced_requests.get(ref_path)
                    if document is None:
                        diagnostics.append(Diagnostic(code="missing-use", message=f"Referenced request definition not found: {step.use}", severity=DiagnosticSeverity.ERROR, location=lookup_location(bundle.suite.source_map, bundle.suite.path, "$")))
                    elif document.raw.get("kind") != "requestDefinition":
                        diagnostics.append(Diagnostic(code="invalid-use-kind", message=f"Referenced file is not a requestDefinition: {step.use}", severity=DiagnosticSeverity.ERROR, location=lookup_location(bundle.suite.source_map, bundle.suite.path, "$")))
        return diagnostics

    def _diag(self, file: Path, source_map: dict, path: str, code: str, message: str) -> Diagnostic:
        return Diagnostic(code=code, message=message, severity=DiagnosticSeverity.ERROR, location=lookup_location(source_map, file, path))
