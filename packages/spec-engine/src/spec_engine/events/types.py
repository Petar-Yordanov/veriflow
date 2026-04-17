from __future__ import annotations

from .base import EngineEvent


def suite_started(**payload): return EngineEvent("suite.started", payload=payload)
def suite_finished(**payload): return EngineEvent("suite.finished", payload=payload)
def test_started(**payload): return EngineEvent("test.started", payload=payload)
def test_finished(**payload): return EngineEvent("test.finished", payload=payload)
def step_started(**payload): return EngineEvent("step.started", payload=payload)
def step_finished(**payload): return EngineEvent("step.finished", payload=payload)
def request_prepared(**payload): return EngineEvent("request.prepared", payload=payload)
def response_received(**payload): return EngineEvent("response.received", payload=payload)
def assertions_evaluated(**payload): return EngineEvent("assertions.evaluated", payload=payload)
def extraction_completed(**payload): return EngineEvent("extraction.completed", payload=payload)
def artifact_saved(**payload): return EngineEvent("artifact.saved", payload=payload)
def validation_error(**payload): return EngineEvent("validation.error", payload=payload)
def runtime_error(**payload): return EngineEvent("runtime.error", payload=payload)
