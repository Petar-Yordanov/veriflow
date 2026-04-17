from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path


@dataclass(slots=True)
class DiscoveryOptions:
    suite_glob: str = "suites/**/*.yml"
    request_glob: str = "requests/**/*.yml"
    environment_glob: str = "environments/**/*.yml"
    fixtures_dir: str = "fixtures"
    artifacts_dir: str = "artifacts"
    enable_conventions: bool = True


@dataclass(slots=True)
class DiscoveryResult:
    project_root: Path
    suites: list[Path] = field(default_factory=list)
    requests: list[Path] = field(default_factory=list)
    environments: list[Path] = field(default_factory=list)
    fixtures_root: Path | None = None
    artifacts_root: Path | None = None


class ProjectDiscovery:
    def __init__(self, options: DiscoveryOptions) -> None:
        self.options = options

    def discover(self, project_root: Path) -> DiscoveryResult:
        project_root = project_root.resolve()
        if not self.options.enable_conventions:
            return DiscoveryResult(project_root=project_root)
        return DiscoveryResult(
            project_root=project_root,
            suites=sorted(project_root.glob(self.options.suite_glob)),
            requests=sorted(project_root.glob(self.options.request_glob)),
            environments=sorted(project_root.glob(self.options.environment_glob)),
            fixtures_root=(project_root / self.options.fixtures_dir),
            artifacts_root=(project_root / self.options.artifacts_dir),
        )
