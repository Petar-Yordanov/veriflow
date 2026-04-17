from __future__ import annotations

from io import StringIO
from pathlib import Path
from typing import Any

import yaml
from yaml import nodes


def load_yaml_text(text: str) -> Any:
    return yaml.safe_load(text)


def load_yaml_file(path: Path) -> Any:
    return load_yaml_text(path.read_text(encoding="utf-8"))


def dump_yaml(value: Any) -> str:
    return yaml.safe_dump(value, sort_keys=False, allow_unicode=True)


def build_source_map_from_text(text: str, base_path: str = "$") -> dict[str, tuple[int, int]]:
    root = yaml.compose(text)
    mapping: dict[str, tuple[int, int]] = {}

    def walk(node: nodes.Node | None, path: str) -> None:
        if node is None:
            return
        mark = getattr(node, "start_mark", None)
        if mark is not None:
            mapping[path] = (mark.line + 1, mark.column + 1)
        if isinstance(node, nodes.MappingNode):
            for key_node, value_node in node.value:
                key = key_node.value
                child = f"{path}.{key}" if path != "$" else f"$.{key}"
                walk(value_node, child)
        elif isinstance(node, nodes.SequenceNode):
            for idx, item in enumerate(node.value):
                walk(item, f"{path}[{idx}]")

    walk(root, base_path)
    return mapping


def normalize_yaml(value: Any) -> Any:
    if isinstance(value, dict):
        return {str(k): normalize_yaml(v) for k, v in value.items()}
    if isinstance(value, list):
        return [normalize_yaml(v) for v in value]
    return value
