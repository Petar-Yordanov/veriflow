from __future__ import annotations

import re

_JSONPATH_ALLOWED = re.compile(r"^\$(?:\.[A-Za-z_][A-Za-z0-9_]*|\[[0-9]+\]|\[\*\])*$")


def is_supported_jsonpath(selector: str) -> bool:
    return bool(_JSONPATH_ALLOWED.match(selector))
