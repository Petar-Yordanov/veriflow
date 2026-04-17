from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from ..models.assertions import AssertionClause, ExpectationSpec
from ..models.results import AssertionEvaluation
from .selectors import SelectorEngine


@dataclass(slots=True)
class AssertionOutcome:
    passed: bool
    evaluations: list[AssertionEvaluation]


class AssertionEngine:
    def __init__(self, selectors: SelectorEngine | None = None) -> None:
        self._selectors = selectors or SelectorEngine()

    def evaluate(self, expect: ExpectationSpec | None, response, timing: dict[str, Any], json_body: Any | None) -> AssertionOutcome:
        evaluations: list[AssertionEvaluation] = []
        if expect is None:
            return AssertionOutcome(True, evaluations)
        if expect.status is not None:
            allowed = expect.status if isinstance(expect.status, list) else [expect.status]
            passed = response.status_code in allowed
            evaluations.append(AssertionEvaluation(target="status", operator="in", expected=allowed, actual=response.status_code, passed=passed, message=None if passed else f"Expected status in {allowed}"))
        if expect.body is not None:
            evaluations.extend(self._evaluate_clause(expect.body, json_body))
        for name, header_expectation in expect.headers.items():
            actual = response.headers.get(name)
            evaluations.extend(self._evaluate_ops(target=f"header:{name}", actual=actual, operators=header_expectation.operators))
        if expect.text is not None:
            body_text = response.text
            evaluations.extend(self._evaluate_ops(target="text", actual=body_text, operators=expect.text.operators))
        if expect.performance is not None:
            for metric, ops in expect.performance.metrics.items():
                evaluations.extend(self._evaluate_ops(target=f"performance:{metric}", actual=timing.get(metric), operators=ops))
        return AssertionOutcome(all(e.passed for e in evaluations), evaluations)

    def _evaluate_clause(self, clause: AssertionClause, json_body: Any) -> list[AssertionEvaluation]:
        evaluations: list[AssertionEvaluation] = []
        if clause.path:
            result = self._selectors.select(json_body, clause.path)
            actual = None if result.missing else result.value
            evaluations.extend(self._evaluate_ops(target=clause.path, actual=actual, operators=clause.operators, missing=result.missing))
        for child in clause.and_:
            evaluations.extend(self._evaluate_clause(child, json_body))
        if clause.or_:
            alternatives = [self._evaluate_clause(child, json_body) for child in clause.or_]
            any_passed = any(all(item.passed for item in alt) for alt in alternatives)
            evaluations.append(AssertionEvaluation(target="or", operator="or", expected=True, actual=any_passed, passed=any_passed, message=None if any_passed else "No OR branch matched"))
            if alternatives:
                evaluations.extend(alternatives[0])
        return evaluations

    def _evaluate_ops(self, *, target: str, actual: Any, operators: dict[str, Any], missing: bool = False) -> list[AssertionEvaluation]:
        out: list[AssertionEvaluation] = []
        for operator, expected in operators.items():
            passed = True
            if operator == "exists":
                passed = bool(expected) != missing
            elif operator == "isNull":
                passed = (actual is None) and not missing if expected else (actual is not None)
            elif operator == "type":
                passed = self._type_name(actual) == expected
            elif operator == "equals":
                passed = actual == expected
            elif operator == "notEquals":
                passed = actual != expected
            elif operator == "in":
                passed = actual in expected
            elif operator == "notIn":
                passed = actual not in expected
            elif operator == "matches":
                passed = isinstance(actual, str) and bool(re.search(str(expected), actual))
            elif operator == "contains":
                passed = expected in actual if isinstance(actual, (list, str)) else False
            elif operator == "notContains":
                passed = expected not in actual if isinstance(actual, (list, str)) else False
            elif operator == "count":
                passed = len(actual) == expected if isinstance(actual, list) else False
            elif operator == "minCount":
                passed = len(actual) >= expected if isinstance(actual, list) else False
            elif operator == "maxCount":
                passed = len(actual) <= expected if isinstance(actual, list) else False
            elif operator == "unique":
                passed = len(actual) == len({repr(x) for x in actual}) if isinstance(actual, list) and expected else False
            elif operator == "greaterThan":
                passed = isinstance(actual, (int, float)) and actual > expected
            elif operator == "greaterThanOrEqual":
                passed = isinstance(actual, (int, float)) and actual >= expected
            elif operator == "lessThan":
                passed = isinstance(actual, (int, float)) and actual < expected
            elif operator == "lessThanOrEqual":
                passed = isinstance(actual, (int, float)) and actual <= expected
            elif operator == "before":
                passed = self._parse_dt(actual) < self._parse_dt(expected)
            elif operator == "after":
                passed = self._parse_dt(actual) > self._parse_dt(expected)
            elif operator == "onOrBefore":
                passed = self._parse_dt(actual) <= self._parse_dt(expected)
            elif operator == "onOrAfter":
                passed = self._parse_dt(actual) >= self._parse_dt(expected)
            else:
                passed = False
            out.append(AssertionEvaluation(target=target, operator=operator, expected=expected, actual=actual, passed=passed, message=None if passed else f"Assertion failed for {target}.{operator}"))
        return out

    def _type_name(self, value: Any) -> str:
        if value is None:
            return "null"
        if isinstance(value, bool):
            return "boolean"
        if isinstance(value, (int, float)):
            return "number"
        if isinstance(value, str):
            return "string"
        if isinstance(value, list):
            return "array"
        if isinstance(value, dict):
            return "object"
        return type(value).__name__

    def _parse_dt(self, value: Any) -> datetime:
        if not isinstance(value, str):
            raise ValueError("Date/time assertions require string values")
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
