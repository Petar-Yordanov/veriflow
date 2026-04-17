from __future__ import annotations

import json
from dataclasses import asdict, is_dataclass
from pathlib import Path

from spec_engine.api import SpecEngine
from spec_engine.discovery.project import DiscoveryOptions, DiscoveryResult
from spec_engine.models.results import SuiteResult
from spec_engine.reporting.base import Reporter
from spec_engine.validation.validator import ValidationResult


class EngineGateway:
    def __init__(self) -> None:
        self._engine = SpecEngine()

    def discover(self, project_root: Path) -> DiscoveryResult:
        return self._engine.discover(project_root, DiscoveryOptions(enable_conventions=True))

    def validate_suite(self, suite_path: Path, environment_path: Path | None = None) -> ValidationResult:
        return self._engine.validate(suite_path=suite_path, environment_path=environment_path)

    async def run_suite(
        self,
        suite_path: Path,
        environment_path: Path | None,
        reporter: Reporter,
        report_json_path: Path | None = None,
    ) -> SuiteResult:
        result = await self._engine.run(
            suite_path=suite_path,
            environment_path=environment_path,
            reporter=reporter,
        )
        if report_json_path is not None:
            report_json_path.parent.mkdir(parents=True, exist_ok=True)
            report_json_path.write_text(json.dumps(_to_jsonable(result), indent=2), encoding="utf-8")
        return result


def _to_jsonable(value):
    if is_dataclass(value):
        return {k: _to_jsonable(v) for k, v in asdict(value).items()}
    if isinstance(value, dict):
        return {k: _to_jsonable(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_to_jsonable(v) for v in value]
    if isinstance(value, Path):
        return str(value)
    return value
