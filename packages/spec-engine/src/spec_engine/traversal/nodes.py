from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable


@dataclass(slots=True)
class TraversalNode:
    path: str
    key: str | int | None
    value: Any
    parent_path: str | None = None

    @property
    def is_mapping(self) -> bool:
        return isinstance(self.value, dict)

    @property
    def is_sequence(self) -> bool:
        return isinstance(self.value, list)


class DocumentTraverser:
    def walk(self, value: Any, base_path: str = "$") -> Iterable[TraversalNode]:
        yield from self._walk(value=value, path=base_path, parent_path=None, key=None)

    def _walk(self, value: Any, path: str, parent_path: str | None, key: str | int | None):
        node = TraversalNode(path=path, key=key, value=value, parent_path=parent_path)
        yield node
        if isinstance(value, dict):
            for child_key, child_value in value.items():
                child_path = f"{path}.{child_key}" if path != "$" else f"$.{child_key}"
                yield from self._walk(child_value, child_path, path, child_key)
        elif isinstance(value, list):
            for index, child_value in enumerate(value):
                child_path = f"{path}[{index}]"
                yield from self._walk(child_value, child_path, path, index)
