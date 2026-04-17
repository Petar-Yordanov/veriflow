from __future__ import annotations

from dataclasses import dataclass
from typing import Any


@dataclass(slots=True)
class SelectorResult:
    value: Any
    missing: bool = False


class SelectorEngine:
    def select(self, document: Any, selector: str) -> SelectorResult:
        tokens = self._tokenize(selector)
        current = [document]
        for token in tokens:
            next_values: list[Any] = []
            if token[0] == "prop":
                name = token[1]
                for item in current:
                    if isinstance(item, dict) and name in item:
                        next_values.append(item[name])
            elif token[0] == "index":
                idx = token[1]
                for item in current:
                    if isinstance(item, list) and 0 <= idx < len(item):
                        next_values.append(item[idx])
            elif token[0] == "wildcard":
                for item in current:
                    if isinstance(item, list):
                        next_values.extend(item)
            current = next_values
        if len(current) == 0:
            return SelectorResult(value=None, missing=True)
        if len(current) == 1:
            return SelectorResult(value=current[0], missing=False)
        return SelectorResult(value=current, missing=False)

    def _tokenize(self, selector: str) -> list[tuple[str, Any]]:
        if selector == "$":
            return []
        if not selector.startswith("$"):
            raise ValueError(f"Invalid selector: {selector}")
        i = 1
        tokens: list[tuple[str, Any]] = []
        while i < len(selector):
            if selector[i] == ".":
                i += 1
                start = i
                while i < len(selector) and (selector[i].isalnum() or selector[i] == "_"):
                    i += 1
                tokens.append(("prop", selector[start:i]))
            elif selector[i] == "[":
                end = selector.index("]", i)
                inside = selector[i + 1:end]
                if inside == "*":
                    tokens.append(("wildcard", None))
                else:
                    tokens.append(("index", int(inside)))
                i = end + 1
            else:
                raise ValueError(f"Invalid selector: {selector}")
        return tokens
