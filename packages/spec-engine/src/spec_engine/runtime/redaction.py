from __future__ import annotations

from collections.abc import Mapping
from copy import deepcopy
from typing import Any

from ..constants import REDACTION_VALUE


class Redactor:
    def redact_mapping(self, values: Mapping[str, Any], sensitive_names: set[str]) -> dict[str, Any]:
        result = dict(values)
        for name in sensitive_names:
            if name in result:
                result[name] = REDACTION_VALUE
        return result

    def redact_value(self, value: Any, sensitive: bool) -> Any:
        if sensitive:
            return REDACTION_VALUE
        return deepcopy(value)
