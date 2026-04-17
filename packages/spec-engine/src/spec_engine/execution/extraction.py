from __future__ import annotations

from typing import Any

from ..models.request import RequestDefinitionSpec
from ..models.results import ExtractionResult
from ..models.suite import ExtractionSpec
from .selectors import SelectorEngine


class ExtractionEngine:
    def __init__(self, selectors: SelectorEngine | None = None) -> None:
        self._selectors = selectors or SelectorEngine()

    def extract(self, extraction_specs: dict[str, ExtractionSpec], request_definition: RequestDefinitionSpec | None, json_body: Any) -> tuple[dict[str, Any], list[ExtractionResult], bool]:
        variables: dict[str, Any] = {}
        results: list[ExtractionResult] = []
        ok = True
        for name, spec in extraction_specs.items():
            selector = spec.from_selector
            if spec.from_definition and request_definition:
                output = request_definition.outputs[spec.from_definition]
                selector = output.path
            result = self._selectors.select(json_body, selector) if selector else None
            if result is None or result.missing:
                passed = not spec.required
                value = None
                ok = ok and passed
                results.append(ExtractionResult(name=name, scope=spec.scope.value, value=value, sensitive=spec.sensitive, missing=True, passed=passed, message=None if passed else f"Required extraction '{name}' missing"))
            else:
                variables[name] = result.value
                results.append(ExtractionResult(name=name, scope=spec.scope.value, value=result.value, sensitive=spec.sensitive, missing=False, passed=True))
        return variables, results, ok
