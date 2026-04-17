from __future__ import annotations

import random
import string
import uuid
from datetime import UTC, datetime


def build_builtin_variables(seed: int, *, suite_id: str | None = None, suite_name: str | None = None, test_id: str | None = None, test_name: str | None = None, step_id: str | None = None, step_name: str | None = None, environment_name: str | None = None, iteration_index: int = 0) -> dict[str, object]:
    rng = random.Random(seed)
    now = datetime.now(UTC)
    return {
        "runId": str(uuid.uuid4()),
        "suiteId": suite_id,
        "suiteName": suite_name,
        "testId": test_id,
        "testName": test_name,
        "stepId": step_id,
        "stepName": step_name,
        "iterationIndex": iteration_index,
        "environmentName": environment_name,
        "currentTimestamp": now.isoformat(),
        "currentIsoTimestamp": now.isoformat(),
        "currentUnixMs": int(now.timestamp() * 1000),
        "randomUuid": str(uuid.uuid4()),
        "randomInt": rng.randint(0, 10_000_000),
        "randomString": ''.join(rng.choice(string.ascii_letters + string.digits) for _ in range(12)),
    }
