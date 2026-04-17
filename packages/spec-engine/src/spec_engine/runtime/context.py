from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from .builtins import build_builtin_variables


@dataclass(slots=True)
class VariableLayers:
    environment: dict[str, Any] = field(default_factory=dict)
    suite_declared: dict[str, Any] = field(default_factory=dict)
    suite_runtime: dict[str, Any] = field(default_factory=dict)
    test_declared: dict[str, Any] = field(default_factory=dict)
    test_runtime: dict[str, Any] = field(default_factory=dict)
    step_declared: dict[str, Any] = field(default_factory=dict)
    inputs: dict[str, Any] = field(default_factory=dict)
    builtins: dict[str, Any] = field(default_factory=dict)

    def as_lookup(self) -> dict[str, Any]:
        merged: dict[str, Any] = {}
        merged.update(self.environment)
        merged.update(self.suite_declared)
        merged.update(self.suite_runtime)
        merged.update(self.test_declared)
        merged.update(self.test_runtime)
        merged.update(self.builtins)
        merged.update(self.step_declared)
        merged["inputs"] = self.inputs
        return merged


class RunContextFactory:
    def create_layers(self, *, seed: int, environment: dict[str, Any], suite_declared: dict[str, Any], suite_runtime: dict[str, Any], test_declared: dict[str, Any], test_runtime: dict[str, Any], step_declared: dict[str, Any], inputs: dict[str, Any], builtin_context: dict[str, Any]) -> VariableLayers:
        builtins = build_builtin_variables(seed=seed, **builtin_context)
        return VariableLayers(
            environment=environment,
            suite_declared=suite_declared,
            suite_runtime=suite_runtime,
            test_declared=test_declared,
            test_runtime=test_runtime,
            step_declared=step_declared,
            inputs=inputs,
            builtins=builtins,
        )
