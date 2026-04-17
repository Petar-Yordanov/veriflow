from __future__ import annotations

import re
from copy import deepcopy
from typing import Any

_FULL = re.compile(r"^\{\{\s*([A-Za-z0-9_\.]+)\s*\}\}$")
_EMBEDDED = re.compile(r"\{\{\s*([A-Za-z0-9_\.]+)\s*\}\}")


class InterpolationError(ValueError):
    pass


class Interpolator:
    def resolve_data(self, value: Any, lookup: dict[str, Any]) -> Any:
        if isinstance(value, dict):
            return {k: self.resolve_data(v, lookup) for k, v in value.items()}
        if isinstance(value, list):
            return [self.resolve_data(v, lookup) for v in value]
        if isinstance(value, str):
            return self.resolve_string(value, lookup)
        return deepcopy(value)

    def resolve_string(self, value: str, lookup: dict[str, Any]) -> Any:
        full = _FULL.match(value)
        if full:
            return self._lookup(full.group(1), lookup)

        def repl(match: re.Match[str]) -> str:
            resolved = self._lookup(match.group(1), lookup)
            if resolved is None or isinstance(resolved, (dict, list)):
                raise InterpolationError(f"Embedded interpolation requires scalar value for '{match.group(1)}'")
            return str(resolved)

        return _EMBEDDED.sub(repl, value)

    def _lookup(self, path: str, lookup: dict[str, Any]) -> Any:
        current: Any = lookup
        for part in path.split('.'):
            if isinstance(current, dict) and part in current:
                current = current[part]
            else:
                raise InterpolationError(f"Unresolved variable '{path}'")
        return current
