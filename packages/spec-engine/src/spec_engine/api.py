from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable

from .artifacts.persistence import ArtifactManager
from .discovery.project import DiscoveryOptions, ProjectDiscovery
from .events.base import EngineEvent
from .execution.runner import EngineRunner, RunnerDependencies, RunnerOptions
from .loading.loader import SpecLoader
from .reporting.base import Reporter
from .reporting.summary import SummaryReporter
from .runtime.context import RunContextFactory
from .validation.validator import SpecValidator, ValidationResult
from .versions.registry import VersionRegistry, build_default_registry


@dataclass(slots=True)
class EngineDependencies:
    registry: VersionRegistry = field(default_factory=build_default_registry)


class SpecEngine:
    def __init__(self, dependencies: EngineDependencies | None = None) -> None:
        self._dependencies = dependencies or EngineDependencies()
        self._loader = SpecLoader(self._dependencies.registry)
        self._validator = SpecValidator(self._dependencies.registry)
        self._artifact_manager = ArtifactManager()

    def discover(self, project_root: Path, options: DiscoveryOptions | None = None):
        return ProjectDiscovery(options or DiscoveryOptions()).discover(project_root)

    def load(self, path: Path):
        return self._loader.load_file(path)

    def validate(self, suite_path: Path, environment_path: Path | None = None) -> ValidationResult:
        loaded = self._loader.load_bundle(suite_path=suite_path, environment_path=environment_path)
        return self._validator.validate_bundle(loaded)

    async def run(
        self,
        suite_path: Path,
        environment_path: Path | None = None,
        reporter: Reporter | None = None,
        options: RunnerOptions | None = None,
        event_sink=None,
    ):
        loaded = self._loader.load_bundle(suite_path=suite_path, environment_path=environment_path)
        validation = self._validator.validate_bundle(loaded)
        reporter = reporter or SummaryReporter()
        dependencies = RunnerDependencies(
            artifact_manager=self._artifact_manager,
            run_context_factory=RunContextFactory(),
        )
        runner = EngineRunner(options=options or RunnerOptions(), dependencies=dependencies)
        return await runner.run(loaded, validation, reporter=reporter, event_sink=event_sink)
