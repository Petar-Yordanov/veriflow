from __future__ import annotations

from pathlib import Path
from typing import Any

from ..loading.yaml_io import dump_yaml


class YamlSerializer:
    def dumps(self, value: Any) -> str:
        return dump_yaml(value)

    def dump_to_file(self, value: Any, path: Path) -> None:
        path.write_text(self.dumps(value), encoding="utf-8")
