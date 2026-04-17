from __future__ import annotations

from pathlib import Path

from ..models.documents import LoadedBundle, LoadedDocument
from ..versions.registry import VersionRegistry
from .yaml_io import build_source_map_from_text, load_yaml_text, normalize_yaml


class SpecLoader:
    def __init__(self, registry: VersionRegistry) -> None:
        self._registry = registry

    def load_file(self, path: Path) -> LoadedDocument:
        text = path.read_text(encoding="utf-8")
        source_map = build_source_map_from_text(text)
        raw = normalize_yaml(load_yaml_text(text))
        handler = self._registry.handler_for_document(raw)
        typed = handler.parse_document(path=path, raw=raw, source_map=source_map)
        return LoadedDocument(path=path, raw=raw, typed=typed, source_map=source_map)

    def load_bundle(self, suite_path: Path, environment_path: Path | None = None) -> LoadedBundle:
        suite = self.load_file(suite_path)
        environment = self.load_file(environment_path) if environment_path else None
        referenced_requests: dict[Path, LoadedDocument] = {}
        if getattr(suite.typed, 'tests', None):
            for test in suite.typed.tests:
                for step in test.steps:
                    if step.use:
                        ref_path = Path(step.use)
                        if not ref_path.is_absolute():
                            ref_path = (suite.path.parent / ref_path).resolve()
                        if ref_path not in referenced_requests:
                            referenced_requests[ref_path] = self.load_file(ref_path)
        return LoadedBundle(suite=suite, environment=environment, referenced_requests=referenced_requests)
