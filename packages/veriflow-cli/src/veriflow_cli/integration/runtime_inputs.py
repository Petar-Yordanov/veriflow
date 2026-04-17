from __future__ import annotations

import json
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml
from spec_engine.api import SpecEngine
from spec_engine.serialization.yaml import YamlSerializer


@dataclass(slots=True)
class PreparedInputs:
    suite_path: Path
    environment_path: Path | None
    temp_paths: list[Path]


class RuntimeInputShaper:
    def __init__(self) -> None:
        self._engine = SpecEngine()
        self._serializer = YamlSerializer()

    def select_environment(self, *, project_root: Path | None, environment_selector: str | None) -> Path | None:
        if not environment_selector:
            return None
        candidate = Path(environment_selector)
        if candidate.exists():
            return candidate.resolve()
        if project_root is None:
            raise ValueError(f"Environment '{environment_selector}' was not a valid path and no project root was given.")
        discovery = self._engine.discover(project_root)
        wanted = environment_selector.strip().lower()
        matches = [p for p in discovery.environments if p.stem.lower() == wanted]
        if not matches:
            raise ValueError(f"Could not find environment '{environment_selector}' under discovered environments.")
        if len(matches) > 1:
            raise ValueError(f"Environment selector '{environment_selector}' matched multiple files.")
        return matches[0]

    def merge_environment_overlays(
        self,
        *,
        base_environment_path: Path | None,
        variable_files: list[Path],
        ad_hoc_vars: list[str],
    ) -> tuple[Path | None, list[Path]]:
        temp_paths: list[Path] = []
        if not base_environment_path and not variable_files and not ad_hoc_vars:
            return None, temp_paths

        merged: dict[str, Any] = {
            "formatVersion": "1.0",
            "kind": "environment",
            "name": "cli-runtime",
            "variables": {},
        }
        if base_environment_path:
            loaded = self._engine.load(base_environment_path)
            if loaded.raw.get("kind") != "environment":
                raise ValueError(f"Selected environment path is not an environment file: {base_environment_path}")
            merged.update({k: v for k, v in loaded.raw.items() if k != "variables"})
            merged["variables"] = dict(loaded.raw.get("variables", {}))

        for vf in variable_files:
            payload = yaml.safe_load(vf.read_text(encoding="utf-8"))
            if payload is None:
                continue
            if isinstance(payload, dict) and payload.get("kind") == "environment":
                payload = payload.get("variables", {})
            if not isinstance(payload, dict):
                raise ValueError(f"Variable file must be a mapping or environment file: {vf}")
            merged["variables"] = _deep_merge(merged["variables"], payload)

        if ad_hoc_vars:
            merged["variables"] = _deep_merge(merged["variables"], _parse_ad_hoc_vars(ad_hoc_vars))

        temp_path = _named_temp_path(prefix="veriflow-env-", suffix=".yml")
        temp_path.write_text(self._serializer.dumps(merged), encoding="utf-8")
        temp_paths.append(temp_path)
        return temp_path, temp_paths

    def filter_suite(self, suite_path: Path, selection) -> tuple[Path, list[Path], str]:
        loaded = self._engine.load(suite_path)
        raw = dict(loaded.raw)
        tests = list(raw.get("tests", []))
        if not selection.is_active:
            return suite_path, [], _display_name(raw, suite_path)
        filtered = [t for t in tests if selection.matches_test(t)]
        raw["tests"] = filtered
        temp_path = _named_temp_path(prefix="veriflow-suite-", suffix=".yml")
        temp_path.write_text(self._serializer.dumps(raw), encoding="utf-8")
        return temp_path, [temp_path], _display_name(raw, suite_path)


def _display_name(raw: dict[str, Any], suite_path: Path) -> str:
    info = raw.get("info") or {}
    return str(info.get("name") or raw.get("name") or suite_path.stem)


def _named_temp_path(*, prefix: str, suffix: str) -> Path:
    handle = tempfile.NamedTemporaryFile(prefix=prefix, suffix=suffix, delete=False)
    path = Path(handle.name)
    handle.close()
    return path


def _deep_merge(left: dict[str, Any], right: dict[str, Any]) -> dict[str, Any]:
    result = dict(left)
    for key, value in right.items():
        if isinstance(result.get(key), dict) and isinstance(value, dict):
            result[key] = _deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def _parse_ad_hoc_vars(items: list[str]) -> dict[str, Any]:
    merged: dict[str, Any] = {}
    for item in items:
        if "=" not in item:
            raise ValueError(f"Invalid --var '{item}'. Expected key=value.")
        key, raw_value = item.split("=", 1)
        value = _parse_scalar_or_json(raw_value)
        cursor = merged
        segments = [segment for segment in key.split(".") if segment]
        if not segments:
            raise ValueError(f"Invalid --var key in '{item}'.")
        for segment in segments[:-1]:
            cursor = cursor.setdefault(segment, {})
            if not isinstance(cursor, dict):
                raise ValueError(f"Conflicting --var path in '{item}'.")
        cursor[segments[-1]] = value
    return merged


def _parse_scalar_or_json(raw_value: str) -> Any:
    lowered = raw_value.lower()
    if lowered == "true":
        return True
    if lowered == "false":
        return False
    if lowered == "null":
        return None
    try:
        return json.loads(raw_value)
    except Exception:
        return raw_value
