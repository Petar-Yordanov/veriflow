from __future__ import annotations

from enum import IntEnum


class ExitCode(IntEnum):
    OK = 0
    VALIDATION_FAILED = 2
    RUNTIME_FAILED = 3
    USAGE_ERROR = 4
    INTERNAL_ERROR = 5
