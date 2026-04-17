from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

from spec_engine.api import SpecEngine

from .runtime_inputs import RuntimeInputShaper


@dataclass(slots=True)
class SelectionOptions:
    test_ids: set[str] = field(default_factory=set)
    test_names: set[str] = field(default_factory=set)
    tags: set[str] = field(default_factory=set)

    @property
    def is_active(self) -> bool:
        return bool(self.test_ids or self.test_names or self.tags)

    def matches_test(self, test: dict) -> bool:
        if not self.is_active:
            return True
        if self.test_ids and test.get("id") in self.test_ids:
            return True
        if self.test_names and test.get("name") in self.test_names:
            return True
        test_tags = set(test.get("tags") or [])
        if self.tags and test_tags.intersection(self.tags):
            return True
        return False


@dataclass(slots=True)
class SuiteRunInput:
    suite_path: Path
    environment_path: Path | None
    display_name: str
    temp_paths: list[Path] = field(default_factory=list)


_engine = SpecEngine()
_shaper = RuntimeInputShaper()


def build_single_suite_run_input(
    *,
    suite_path: Path,
    project_root: Path | None,
    environment_selector: str | None,
    variable_files: list[Path],
    ad_hoc_vars: list[str],
    selection: SelectionOptions,
) -> SuiteRunInput:
    discovered_root = project_root or _infer_project_root(suite_path)
    env_path = _shaper.select_environment(project_root=discovered_root, environment_selector=environment_selector)
    env_path, env_temps = _shaper.merge_environment_overlays(
        base_environment_path=env_path,
        variable_files=variable_files,
        ad_hoc_vars=ad_hoc_vars,
    )
    suite_path2, suite_temps, display_name = _shaper.filter_suite(suite_path, selection)
    return SuiteRunInput(
        suite_path=suite_path2,
        environment_path=env_path,
        display_name=display_name,
        temp_paths=[*env_temps, *suite_temps],
    )


def build_discovered_run_inputs(
    *,
    project_root: Path,
    environment_selector: str | None,
    variable_files: list[Path],
    ad_hoc_vars: list[str],
    suite_ids: set[str],
    suite_names: set[str],
    selection: SelectionOptions,
) -> list[SuiteRunInput]:
    discovery = _engine.discover(project_root)
    items: list[SuiteRunInput] = []
    for suite_path in discovery.suites:
        loaded = _engine.load(suite_path)
        suite_name = (loaded.raw.get("info") or {}).get("name") or loaded.raw.get("name") or suite_path.stem
        suite_id = loaded.raw.get("id") or (loaded.raw.get("info") or {}).get("id")
        if suite_ids and suite_id not in suite_ids:
            continue
        if suite_names and suite_name not in suite_names:
            continue
        items.append(
            build_single_suite_run_input(
                suite_path=suite_path,
                project_root=project_root,
                environment_selector=environment_selector,
                variable_files=variable_files,
                ad_hoc_vars=ad_hoc_vars,
                selection=selection,
            )
        )
    return items


def _infer_project_root(suite_path: Path) -> Path:
    current = suite_path.resolve().parent
    for candidate in [current, *current.parents]:
        if (candidate / "suites").exists() or (candidate / "requests").exists() or (candidate / "environments").exists():
            return candidate
    return current
