# Veriflow stability track

Veriflow 1.0 freezes the core HTTP/pipeline runner contract. The goal is deterministic, safe, diagnosable behavior that can be left in CI for a long time, not an attempt to include every possible API-testing feature.

## Compatibility surfaces

Treat these as public contracts even before 1.0:

- YAML `formatVersion` semantics and request/environment conventions.
- CLI command/option names and stdout/stderr behavior.
- Process exit codes (`0`, `2`, `3`, `4`, `5`).
- Logical report `statusCode` (`0` success, `-1` failed).
- Stable `VF####` diagnostic codes and symbolic diagnostic names.
- JSON report schema and JSONL event schema/event names.
- Optional JUnit XML mapping. This is an XML interoperability format generated directly by Go (`encoding/xml`); Veriflow has no Java runtime dependency.
- Variable precedence, lifetime, extraction scope, and redaction behavior.
- Selection, named-case identities, and sharding semantics.
- Timeout and teardown semantics.

Breaking semantics should require an explicitly versioned migration rather than silently changing existing `1.0` behavior.

## Stability work now present

### Reporting and CI status

- Suite and consolidated JSON results carry `statusCode: 0` or `statusCode: -1`.
- OS process exit remains portable and explicit: `0` success, `2` validation, `3` executed test/runtime failure, `4` CLI usage/configuration, `5` unexpected/internal or requested-report failure.
- Requested final JSON/JUnit outputs are attempted before the CLI returns a test/validation failure code.
- Reporter/event-sink failures are no longer swallowed; they make the invocation fail.
- Validation failures from `run` can be written as failed JSON/JUnit/event outputs before exit.
- Event JSONL is initialized once per invocation so repeated runs do not silently append stale previous-run data.

### Parsing and validation

- Default YAML parsing uses YAML-org `go.yaml.in/yaml/v3` rather than Veriflow's original home-grown syntax parser.
- Veriflow retains strict single-document, UTF-8, duplicate-key, string-key, nesting/alias, size, and source-location checks around the mature parser.
- Block/folded scalars, Unicode, anchors/aliases, merge keys, flow collections, and normal YAML mappings/sequences are supported.
- Whole-project validation includes unused requests and environments, cross-file request references/contracts, environment inheritance, duplicate IDs/names, nested field/type/range checks, selectors, assertions, retries, body modes, and safely-known file paths.
- User-caused spec/project failures are represented as stable structured diagnostics instead of leaking into the internal-error path.
- Optional `veriflow.yml` project configuration is strictly validated and has its own exported JSON Schema.

### Sensitive data

- Sensitive CLI environment imports, request inputs, auth material, and sensitive extractions register with a run-wide redactor.
- Common secret-bearing HTTP headers are structurally redacted.
- Short one/two-character sensitive values are never deliberately exempted. When a very short secret is embedded in a larger free-form string and cannot be safely substring-redacted, Veriflow conservatively redacts the containing value.
- Sensitive values discovered by extraction are registered before response body/header log events are emitted, preventing first-response event leakage.
- JSON reports, events, assertion data, suite/test/step errors, response previews, and Veriflow-managed artifacts pass through the redactor.

### Lifecycle and timeouts

- Timeout hierarchy exists at run, suite, test, step, and HTTP-request levels.
- `afterEach` and `afterAll` are attempted after ordinary failures and after parent cancellation/timeouts.
- Teardown runs in an independent bounded cleanup context (default 15 seconds), so cancellation cannot silently skip cleanup or permit unlimited cleanup execution.
- SIGINT/SIGTERM cancel normal execution.

### Data cases and selectors

- Data-driven tests use deterministic **named case mappings**, producing stable IDs such as `authorization[admin]` rather than position-based anonymous iterations.
- Cases are independently visible in results/JUnit and support case variables/skip behavior.
- The documented Veriflow JSONPath dialect includes child/quoted access, wildcards, negative indexes, unions, slices, recursive descent/unions/wildcards, comparison/existence filters, root references, regex filters, and logical filter expressions.
- Selector validation rejects malformed visible selectors before execution.

### Runtime safety and project operation

- Bounded HTTP response bodies, finite transport timeouts, reusable connection pools, and context-aware waits/retry backoff.
- Request/body/artifact path confinement including symlink escape checks.
- Recursive variable expansion with deterministic cycle detection and bounded depth.
- Required/typed/default/sensitive request inputs and output contracts.
- Environment inheritance and process-environment imports.
- Suite defaults, lifecycle hooks, retries, repeat/warmup, extraction, artifacts, logging, TLS/custom CA/mTLS/proxy controls, basic/bearer/API-key auth, and optional run-scoped cookie jar.
- JUnit, per-suite JSON, consolidated JSON, and JSONL event reporting.

## CI quality gate

The repository CI is expected to run the **default mature-YAML build** and enforces:

- `go mod download` + `go mod verify`;
- `gofmt`;
- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- at least 70% aggregate coverage across `./internal/...`;
- Windows x64, Linux x64, macOS x64, and macOS arm64 test/build coverage;
- self-contained positive pipeline smoke tests;
- separately logged expected-failure contracts;
- native release binary/version/schema smoke checks.

Coverage is a regression floor, not a claim that a percentage alone proves correctness. Error-path tests should continue to be added whenever a production bug or difficult boundary is found.

## Deliberately deferred for now

These are not part of the current stabilization scope:

- OAuth2/token-refresh automation;
- controlled parallel suite execution;
- streaming extremely large upload/download bodies;
- signed release artifacts, SBOMs, or provenance;
- sustained fuzz campaigns;
- OpenAPI generation;
- snapshot/history/rerun-failed systems;
- WebSocket/gRPC support;
- advanced load/performance testing;
- more elaborate sharding.

They can be added later when there is a concrete use case without weakening the compatibility and reliability contracts above.

## 1.0 compatibility rule

For the 1.0 line, the default CI path must remain green with the mature YAML dependency, cross-platform tests, race detector, positive/negative pipeline suites, report/error-path tests, and the documented compatibility contracts above. Incompatible changes to YAML, CLI behavior, process/report status semantics, diagnostics, reports/events, selector behavior, or variable/runtime semantics require an explicitly versioned migration rather than a silent behavioral change.
