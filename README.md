# Veriflow

Veriflow is a standalone Go CLI for convention-based, YAML-defined HTTP/API tests. It is designed to run the same way on a developer machine and in CI/CD pipelines, without Python, Poetry, PyInstaller, or a runtime service dependency.

The repository includes a self-contained fixture API and pipeline suites so Veriflow can test itself without calling the public internet.

## Status

The current spec format is exactly `1.0`. The CLI, exit codes, report schema, and event names should be treated as compatibility surfaces when changing Veriflow.

Current release version is stored in `VERSION`. Release builds inject the version, Git commit, and build timestamp into the binary.

## Project convention

A Veriflow project uses these folders:

```text
project/
├── environments/
│   ├── dev.yml
│   └── ci.yml
├── requests/
│   ├── health/get.yml
│   └── resources/create.yml
├── suites/
│   └── smoke.yml
├── fixtures/
│   └── payload.json
└── artifacts/
```

Both `.yml` and `.yaml` are discovered recursively.

Request references are logical names rooted at `requests/`:

```yaml
use: "resources/create"
```

Do not use filesystem traversal such as `../requests/resources/create.yml`.

Environments are selected by name:

```bash
veriflow run discovered . -e ci
```

`-e ci` resolves from `environments/ci.yml`. Environment paths are intentionally rejected.

## Build and test

Veriflow 1.0 targets Go 1.25 or newer. The production YAML parser is the pure-Go `go.yaml.in/yaml/v3` dependency.

```bash
go test ./...
go test -race ./...
go vet ./...
go build -o veriflow ./cmd/veriflow
```

Self-contained pipeline checks:

```bash
bash ./scripts/pipeline-smoke.sh
bash ./scripts/pipeline-negative.sh
bash ./scripts/pipeline-all.sh
```

`pipeline-smoke.sh` contains only successful E2E scenarios. Expected failures are kept in `pipeline-negative.sh` so normal smoke logs are not polluted with intentional errors.

## CLI

```text
veriflow discover suites PROJECT_ROOT [--json]
veriflow discover requests PROJECT_ROOT [--json]
veriflow discover environments PROJECT_ROOT [--json]
veriflow discover tests PROJECT_ROOT [--json]
veriflow schema [spec|config] [--output FILE]

veriflow validate file SUITE [--project-root ROOT] [-e NAME] [--json]
veriflow validate project PROJECT_ROOT [--json]

veriflow run suite SUITE [options]
veriflow run discovered PROJECT_ROOT [options]

veriflow version [--json]
```

Important pipeline options:

```text
--environment NAME, -e NAME
--var key=value
--var-file FILE
--var-from-env NAME[=ENV_NAME]
--secret-var-from-env NAME[=ENV_NAME]
--test-id ID
--test-name NAME
--tag TAG
--exclude-test-id ID
--exclude-test-name NAME
--exclude-tag TAG
--suite-id ID
--suite-name NAME
--exclude-suite-id ID
--exclude-suite-name NAME
--shard INDEX/TOTAL
--fail-fast
--fail-if-no-tests
--no-fail-if-no-tests
--ci
--event-jsonl FILE
--report-json FILE
--report-dir DIR
--report-junit FILE
--report-consolidated-json FILE
--max-response-bytes N
--run-timeout-ms N
--suite-timeout-ms N
--test-timeout-ms N
--cleanup-timeout-ms N
--artifacts-root DIR
--cookie-jar
--no-cookie-jar
--ca-file FILE
--client-cert FILE
--client-key FILE
--proxy URL
--insecure-skip-tls-verify
--verify-tls
--json
```

Options that can be repeated, such as `--var`, `--tag`, and `--test-id`, may be provided multiple times.

## Stable exit codes

| Code | Meaning |
|---:|---|
| `0` | Success |
| `2` | Spec/project validation failed |
| `3` | Test/runtime execution failed |
| `4` | Invalid CLI usage or CLI-supplied configuration |
| `5` | Internal/unexpected error |

CI integrations should use the exit code rather than parsing console text.

JSON results also expose a deliberately simple logical `statusCode`: `0` when the suite/run is successful and `-1` when any suite/run failed (including validation/hook failures). This is report data, not the literal OS process code: `os.Exit(-1)` is not portable and appears as `255` on Unix-like systems. Veriflow therefore keeps the stable process exit contract above while writing `statusCode: -1` into failed machine-readable reports. Requested JSON/JUnit/event outputs are attempted before the CLI returns its final non-zero process exit. If a requested report sink itself fails, the invocation fails with exit `5` after the other requested final reports have been attempted.

## Optional project configuration

Convention-only projects require no config file. For stable CI defaults, `PROJECT_ROOT/veriflow.yml` can define operational settings while CLI flags remain the highest-precedence override:

```yaml
runtime:
  runTimeoutMs: 900000
  suiteTimeoutMs: 300000
  testTimeoutMs: 120000
  cleanupTimeoutMs: 15000
  maxResponseBytes: 10485760
  artifactsRoot: artifacts

reports:
  json: .tmp/suite.json
  junit: .tmp/junit.xml
  consolidatedJson: .tmp/veriflow.json
  reportDir: .tmp/suites
  eventJsonl: .tmp/events.jsonl

ci:
  failIfNoTests: true

network:
  caFile: certs/internal-ca.pem
  clientCert: certs/client.pem
  clientKey: certs/client-key.pem
  proxy: http://proxy.internal:8080
  cookieJar: true
  insecureSkipTlsVerify: false
```

Relative file/report/artifact paths in the config are resolved from the project root. CLI values override config values; boolean defaults can be explicitly disabled with `--no-fail-if-no-tests`, `--no-cookie-jar`, and `--verify-tls`. `reports.json` applies to `run suite`, while `reports.consolidatedJson` applies to `run discovered`; both may coexist in one project config. Export the strict project-config schema with:

```bash
veriflow schema config --output veriflow.project.schema.json
```

Unknown fields and invalid config value types/ranges are validation errors with stable diagnostics; they are not silently ignored.

## Environment files and variables

```yaml
formatVersion: "1.0"
kind: environment
name: ci
variables:
  serverOrigin: "http://127.0.0.1:18080"
  apiBaseUrl: "{{serverOrigin}}"
  apiAlias: "{{apiBaseUrl}}"
  expectedStatus: "Open"
```

Variable aliases are recursively expanded before a request or assertion is evaluated. Cycles fail explicitly:

```yaml
variables:
  a: "{{b}}"
  b: "{{a}}"
```

produces an error such as:

```text
Variable expansion cycle detected: a -> b -> a
```

Environments may inherit another discovered environment by name:

```yaml
formatVersion: "1.0"
kind: environment
name: ci
extends: base
variables:
  pipelineName: ci
```

Parent variables are deep-merged first and the child overrides them. Inheritance cycles and missing parents fail project validation.

The current precedence from lower to higher is:

```text
environment
suite-declared
suite-runtime extraction
test-declared
test-runtime extraction
builtins
step-declared
inputs (available under inputs.*)
```

CLI variable overrides are merged into the selected environment before the runtime layers are built.

### Pipeline variables from the process environment

Use process environment variables instead of putting secrets on the command line:

```bash
export API_TOKEN='...'

veriflow run discovered . -e ci \
  --secret-var-from-env apiToken=API_TOKEN
```

A non-secret environment import is also available:

```bash
veriflow run discovered . \
  --var-from-env buildNumber=BUILD_NUMBER
```

Values imported with `--var-from-env` are parsed as JSON scalars/objects/arrays when possible. Values imported with `--secret-var-from-env` remain strings and are registered with the redactor.

## Sensitive values and redaction

Request inputs can be marked sensitive:

```yaml
inputs:
  token:
    type: string
    required: true
    sensitive: true
```

Sensitive values are sent to the API normally but are redacted from runtime events and result summaries. Veriflow also treats common secret-bearing HTTP headers such as `Authorization`, `Cookie`, `Proxy-Authorization`, API-key/token/secret headers as sensitive.

Sensitive extracted values can be marked the same way:

```yaml
extract:
  accessToken:
    from: "$.accessToken"
    scope: test
    sensitive: true
```

The redaction marker is:

```text
*******
```

Response/body/header artifacts pass through the same run redactor. Do not rely on redaction as a replacement for normal CI secret-management controls; avoid deliberately writing secrets to files that Veriflow does not manage.

## Request definitions

```yaml
formatVersion: "1.0"
kind: requestDefinition
id: "resources/create"

inputs:
  ownerId:
    type: string
    required: true
  resourceName:
    type: string
    required: true
    default: "pipeline-resource"

request:
  method: POST
  baseUrl: "{{apiAlias}}"
  path: "/api/resources"
  headers:
    X-Correlation-ID: "{{correlationId}}"
  body:
    ownerId: "{{inputs.ownerId}}"
    name: "{{inputs.resourceName}}"

outputs:
  resourceId:
    path: "$.data.id"
    required: true
```

Supported input types are:

```text
any
string
number
integer / int
boolean / bool
object / map
array / list
null
```

Unknown inputs, missing required inputs, and type mismatches fail rather than being silently ignored.

## Suite example

```yaml
formatVersion: "1.0"
kind: testSuite
id: smoke

info:
  name: Smoke

globals:
  variables:
    ownerAlias: "{{defaultOwnerId}}"

defaults:
  timeoutMs: 5000
  followRedirects: true
  headers:
    X-Test-Source: veriflow
  retry:
    count: 2
    delayMs: 100
    backoffMultiplier: 2
    maxDelayMs: 1000
    when:
      statusIn: [502, 503, 504]
      networkErrors: true
      timeouts: true

tests:
  - id: lifecycle
    tags: [smoke, pipeline]
    steps:
      - id: create
        use: "resources/create"
        with:
          ownerId: "{{ownerAlias}}"
        expect:
          status: 201
        extract:
          createdId:
            fromDefinition: resourceId
            scope: test
            required: true

      - id: get
        use: "resources/get"
        variables:
          resourceId: "{{createdId}}"
        expect:
          status: 200
          body:
            path: "$.data.id"
            equals: "{{createdId}}"
```

## Named data cases

A test can define a deterministic mapping of named cases. Named mappings are used instead of an anonymous `foreach` list so adding/reordering cases does not silently renumber pipeline identities:

```yaml
tests:
  - id: authorization
    name: Authorization
    cases:
      admin:
        variables:
          role: admin
          expectedStatus: 200
      guest:
        variables:
          role: guest
          expectedStatus: 403
    steps:
      - id: check
        request:
          method: GET
          url: "{{apiAlias}}/api/access/{{role}}"
        expect:
          status: "{{expectedStatus}}"
```

This expands into independent test identities such as `authorization[admin]` and `authorization[guest]`. Cases are sorted by case ID for deterministic execution/report order, appear as separate test results/JUnit test cases, can override test variables, and can be individually skipped. `--test-id authorization` selects all of its cases; the expanded ID selects one case. Case IDs are intentionally restricted to stable identifier characters (`A-Z`, `a-z`, `0-9`, `.`, `_`, `-`).

## Lifecycle hooks

Suites can define setup/teardown hooks using the same step model as normal test steps:

```yaml
hooks:
  beforeAll:
    - id: create-fixture
      use: "resources/create"
      with:
        ownerId: "{{defaultOwnerId}}"
        resourceName: "hook-fixture"
      extract:
        fixtureId:
          fromDefinition: resourceId
          scope: suite
          required: true

  beforeEach:
    - id: settle
      wait:
        forMs: 25

  afterEach:
    - id: after-test
      wait:
        forMs: 5

  afterAll:
    - id: delete-fixture
      use: "resources/delete"
      variables:
        resourceId: "{{fixtureId}}"
      expect:
        status: 204
```

`beforeAll` runs once before selected non-skipped execution begins. A failed `beforeAll` prevents test bodies from running. `beforeEach` and `afterEach` wrap each non-skipped test; a failed `beforeEach` skips that test body. Teardown is still attempted after normal failures **and after the parent run/test context is cancelled**: `afterEach`/`afterAll` receive a detached cleanup context with a bounded `cleanupTimeoutMs` (default 15 seconds) so Ctrl+C, SIGTERM, or a suite/test timeout cannot silently skip cleanup or let it run forever. Hook steps honor `continueOnFailure`, retries, waits, assertions, extraction, redaction, logging, and artifacts like normal steps.

Suite-scoped values extracted by hooks are available to later hooks and tests. Test-scoped values extracted by `beforeEach` are available to the test body and `afterEach`. Hook results are retained in JSON reports, and hook failures are surfaced in JUnit. Skipped tests do not run per-test hooks.

## Request shapes

A request may use either an absolute `url`, or `baseUrl` plus `path`:

```yaml
request:
  method: GET
  baseUrl: "{{apiAlias}}"
  path: "/api/users/{userId}"
  pathParams:
    userId: "{{userId}}"
  pathParamEncoding:
    enabled: true
  query:
    expand: details
  headers:
    Accept: application/json
  cookies:
    locale: en
```

Query and form values may be arrays; Veriflow emits repeated parameters in deterministic order. Cookie values may be declared directly on a request, and `--cookie-jar` enables server-set cookies to persist across later requests in the same run.

Only one body mode may be used on a request.

### JSON body

```yaml
body:
  name: "{{name}}"
  active: true
```

### Raw body

Block scalar bodies are supported:

```yaml
bodyRaw: |
  {
    "name": "{{name}}"
  }
```

### External files

```yaml
bodyFile: "fixtures/payload.bin"
bodyFileMode: binary
```

Modes:

- `binary` — bytes are sent unchanged.
- `text` — placeholders inside the file text are expanded.
- `json` — JSON is decoded, recursively interpolated while preserving scalar types, then encoded again.

File paths are confined to the project root, including checks against symlink escapes.

### Form body

```yaml
form:
  name: "{{name}}"
  count: 3
```

### Multipart

```yaml
multipart:
  note: "{{pipelineName}}"
  document:
    file: "fixtures/payload.txt"
    filename: "payload.txt"
    contentType: "text/plain"
```

Multipart scalar fields may also be arrays to send repeated values.


## Authentication

Veriflow supports first-class basic, bearer, and API-key authentication. Auth values are interpolated like other request fields, and credentials are automatically registered as sensitive runtime values.

Bearer:

```yaml
auth:
  type: bearer
  token: "{{apiToken}}"
```

Basic:

```yaml
auth:
  type: basic
  username: "{{username}}"
  password: "{{password}}"
```

API key in a header:

```yaml
auth:
  type: apiKey
  name: X-API-Key
  value: "{{apiKey}}"
  in: header
```

API key in the query string:

```yaml
auth:
  type: apiKey
  name: api_key
  value: "{{apiKey}}"
  in: query
```

For CI credentials, pair these with `--secret-var-from-env` rather than putting secrets directly in YAML.

## Assertions

Status:

```yaml
expect:
  status: [200, 204]
```

JSON-body selectors use the documented Veriflow JSONPath dialect. It intentionally has explicit supported semantics rather than claiming compatibility with every JSONPath implementation. Current coverage includes:

```text
$                                root
$.property                       child property
$['dotted.key']                  quoted property
$["key-with-dashes"]            quoted property
$.items[0] / $.items[-1]         positive/negative index
$.items[*] / $.*                 array/object wildcard
$.items[0,2,-1]                  index union
$['a','b']                       property union
$.items[1:5]                     slice
$.items[::-1]                    negative-step slice
$..id / $..['id']                recursive property
$..['id','code']                 recursive property union
$..*                             recursive wildcard
$.users[?(@.active)]              existence/truthiness filter
$.users[?(@.age >= 18)]           numeric/string comparisons
$.users[?(@.role == 'admin')]     literal comparisons
$.users[?(@.age >= $.minimumAge)] root-reference comparisons
$.users[?(@.name =~ /^a/i)]       regex filters (i/m/s flags)
$.users[?(@.active && @.age > 20)] logical && / || / ! / parentheses
```

Filters support `==`, `!=`, `>`, `>=`, `<`, `<=`, `=~`, booleans, `null`, strings, numbers, relative `@` paths, root `$` paths, `&&`, `||`, `!`, and parentheses. Object wildcard traversal is sorted for deterministic output. Missing values are distinct from explicit JSON `null`. Malformed selector/filter syntax is rejected during validation when it is statically visible.

Examples:

```yaml
expect:
  body:
    and:
      - path: "$.data.id"
        equals: "{{createdId}}"
      - path: "$.data.status"
        in: [Open, Closed]
      - path: "$.data.tags"
        contains: beta
        minCount: 1
        unique: true
```

Supported operators:

```text
exists
isNull
type
equals
notEquals
equalsIgnoreCase
in
notIn
matches
contains
notContains
startsWith
endsWith
count
minCount
maxCount
length
minLength
maxLength
unique
greaterThan
greaterThanOrEqual
lessThan
lessThanOrEqual
before
after
onOrBefore
onOrAfter
sha256
```

Expected assertion values are interpolated before comparison.

Header assertions:

```yaml
headers:
  Content-Type:
    contains: application/json
```

Text assertions:

```yaml
text:
  contains: "{{expectedText}}"
  matches: "^ready:"
```

Performance assertions use `totalMs`, `avgMs`, `p95Ms`, or `maxMs`:

```yaml
performance:
  totalMs:
    lessThan: 1000
```

## Extraction

Direct selector:

```yaml
extract:
  userId:
    from: "$.data.id"
    scope: test
    required: true
```

Request-defined output:

```yaml
extract:
  userId:
    fromDefinition: resourceId
    scope: test
```

Other response sources are first-class as well:

```yaml
extract:
  traceId:
    fromHeader: X-Trace-ID
  session:
    fromCookie: session
    sensitive: true
  ticket:
    fromTextRegex: "ticket=(.+)"
  statusCode:
    fromStatus: true
```

`required` and `sensitive` flags declared on a request-definition output are inherited when that output is consumed through `fromDefinition`.

Scopes currently accepted by the format are `suite`, `test`, and `step`. Suite values survive across tests in the same suite; test values survive across later steps in the same test. Step-scoped values are retained in the step result but intentionally do not leak into later steps.

## Retries

```yaml
retry:
  count: 3
  delayMs: 100
  backoffMultiplier: 2
  maxDelayMs: 2000
  when:
    statusIn: [429, 502, 503, 504]
    networkErrors: true
    timeouts: true
```

Retries and retry reasons are included in step results. Context cancellation interrupts retry delays.

## Repeat and warmup

```yaml
repeat:
  warmupCount: 1
  count: 5
```

`{{iterationIndex}}` is updated for each measured iteration (`0..count-1`). Warmups use negative iteration indexes and are excluded from measured timing statistics.

## Wait-only steps

```yaml
- id: allow-eventual-consistency
  wait:
    forMs: 250
```

Requests can also use `wait.beforeMs` and `wait.afterMs`.


## Request/response logging

Logging is opt-in per step:

```yaml
log:
  request:
    headers: true
    body: true
  response:
    headers: true
    body: true
```

The resulting `request.log` and `response.log` events pass through the same sensitive-value/header redactor as other runtime events.

## Artifacts

```yaml
artifacts:
  saveResponseBodyTo: "responses/body.txt"
  saveParsedJsonTo: "responses/body.json"
  saveHeadersTo: "responses/headers.json"
  saveTimingTo: "responses/timing.json"
```

Artifact paths are confined to the configured artifact root and reject symlink escapes.

## Reports for CI

### Per-suite JSON

```bash
veriflow run suite suites/smoke.yml -e ci \
  --report-json .tmp/smoke.json
```

### Per-suite directory for discovered runs

```bash
veriflow run discovered . -e ci \
  --report-dir .tmp/reports
```

### One aggregate JSON report

```bash
veriflow run discovered . -e ci \
  --report-consolidated-json .tmp/veriflow.json
```

Aggregate reports have an explicit `schemaVersion` and contain both the consolidated totals and individual suite results. Their consolidated `statusCode` is `-1` if any suite failed and `0` otherwise. Suite JSON reports use the same `-1`/`0` logical status.

Validation failures requested through `run` are reportable too: Veriflow writes a failed synthetic validation result (`statusCode: -1`), JUnit receives a `validation` failure case, and requested event JSONL receives `validation.error` events before the CLI returns validation exit code `2`. Event JSONL is truncated once at the start of an invocation so rerunning into the same path does not silently mix runs.

### JUnit XML

```bash
veriflow run discovered . -e ci \
  --report-junit .tmp/junit.xml
```

JUnit here means the standard JUnit XML **report format** only. Veriflow generates that XML directly in Go with the standard library (`encoding/xml`); it does not contain or require Java. The report is optional and is intended for native test-result publishing in CI systems.

### Event stream

```bash
veriflow run discovered . -e ci \
  --event-jsonl .tmp/events.jsonl
```

Events are emitted as one JSON object per line and include `schemaVersion` and `engineVersion` metadata.

## Validation

Validate one suite and its referenced request definitions:

```bash
veriflow validate file suites/smoke.yml -e ci
```

Validate the whole convention-based project:

```bash
veriflow validate project .
```

Project validation checks discovered suites, including cross-file request contracts, and also independently validates request definitions and environments even when they are not referenced by a suite. Duplicate suite/request IDs and duplicate environment names are reported deterministically.

Validation also checks nested unknown fields, HTTP methods, URL shape where values are literal, selector syntax, retry/repeat/wait ranges, request input contracts, assertion operators, regular expressions that can be checked statically, body-mode conflicts, and body-file confinement/existence where the path is known before runtime.

User-caused spec/project problems use stable namespaced diagnostic codes (`VF####`) plus a symbolic `name`, message, file, document path, and line/column when available. Examples include `VF1001` YAML parse errors, `VF1101` unknown fields, `VF1302` invalid HTTP methods, `VF1401` invalid selectors, and `VF1602` unknown `veriflow.yml` fields. Exit `5` is reserved for unexpected/internal or requested-report I/O failures rather than malformed user specs.

Spec inputs are parsed by the YAML-org `go.yaml.in/yaml/v3` implementation with Veriflow safety/semantic checks layered on top. UTF-8 is required, duplicate explicit mapping keys are rejected, individual spec files are capped at 10 MiB, nesting/alias use is bounded, only one YAML document is accepted, and Veriflow mappings require string keys. Normal YAML block/folded scalars, Unicode, anchors/aliases, merge keys, quoted/unquoted scalars, flow collections, mappings, and sequences are supported. Source locations from the YAML node tree are retained for diagnostics.

## HTTP runtime safeguards

The default client has finite network/TLS/response-header timeouts and a reusable connection pool. It respects the standard process proxy environment through Go's HTTP transport.

Response bodies are capped at 10 MiB by default. Override the cap when necessary:

```bash
veriflow run discovered . --max-response-bytes 52428800
```

Timeouts form a bounded hierarchy:

```text
run timeout (CLI/project config)
  └─ suite timeout (suite timeoutMs or CLI/config default)
      └─ test timeout (test timeoutMs or CLI/config default)
          └─ step timeout (step timeoutMs)
              └─ HTTP request timeout (request/default timeoutMs)
```

A narrower child deadline can end work before its parent budget is exhausted. `cleanupTimeoutMs` is separate: teardown gets its own bounded detached cleanup window after cancellation. SIGINT and SIGTERM cancel active normal execution. Waits and retry delays are context-aware.


## TLS, mTLS, and proxies

The CLI can configure the HTTP transport for pipeline/private-network use:

```bash
veriflow run discovered . -e ci \
  --ca-file ./certs/internal-ca.pem \
  --client-cert ./certs/client.pem \
  --client-key ./certs/client-key.pem \
  --proxy http://proxy.internal:8080
```

`--client-cert` and `--client-key` must be supplied together. Without `--proxy`, the standard `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment behavior from Go's HTTP transport is used.

For development-only endpoints with intentionally untrusted certificates, `--insecure-skip-tls-verify` is available as an explicit opt-in. It should not be the normal CI configuration.

Session-oriented APIs can opt into a standard in-memory cookie jar:

```bash
veriflow run discovered . -e ci --cookie-jar
```

The jar is run-scoped and is disabled by default so suites do not acquire hidden HTTP state accidentally.

## Built-in variables

The runtime currently exposes values including:

```text
runId
suiteId
suiteName
testId
testName
stepId
stepName
environmentName
iterationIndex
currentTimestamp
currentIsoTimestamp
currentUnixMs
randomUuid
randomInt
randomString
```

`runId` remains stable for all steps in one engine run.

## GitHub Actions pipeline

The repository CI performs separate gates for:

1. module download/verification, formatting, tests (including mature-YAML parser tests), race detector, vet, and a 70% internal coverage floor;
2. normal tests across Windows x64, Linux x64, macOS x64, and macOS arm64;
3. positive self-contained E2E smoke scenarios;
4. expected-failure contract scenarios;
5. native release builds for all four platforms;
6. release binary smoke/version checks;
7. release archives plus SHA-256 checksums.

Large pipeline suites can be split deterministically without changing the YAML:

```bash
veriflow run discovered . -e ci --ci --shard 1/4
```

Suite assignment is based on the normalized relative suite path, so the same repository and shard count always select the same suites. `--fail-if-no-tests`/`--ci` prevents a shard or filter typo from silently succeeding with zero tests.

## Compatibility rules

Before declaring a future `1.0`, treat these as versioned public contracts:

- YAML `formatVersion` behavior;
- CLI command/option names;
- exit codes;
- JSON report schema;
- JSONL event names and payload shape;
- diagnostic codes;
- variable precedence and lifetime;
- request-reference/environment conventions.

Breaking semantics should move to a new format/report version rather than silently changing existing `1.0` behavior.

## Known scope boundaries

Veriflow is an API functional/integration test runner, not a general scripting language or a load-testing replacement. The implementation intentionally does not execute arbitrary shell/code from YAML.

The current deliberately deferred areas are features you said are not needed for this stabilization pass: OAuth2/token-refresh automation, controlled parallel execution, streaming extremely large upload/download bodies, release signing/SBOM/provenance, and sustained fuzz campaigns. They are not required for the current functional HTTP/pipeline use case and should be added only when there is a concrete need.

The 1.0 stability focus is regression/error-path coverage and preserving the frozen diagnostic/report/event/CLI/spec contracts. Veriflow is not trying to add OpenAPI generation, snapshot/history systems, rerun-failed state, WebSocket/gRPC, advanced load testing, or more elaborate sharding in this pass.

`STABILITY.md` records the Veriflow 1.0 compatibility surfaces and the quality gates that protect them. See the executable suites under `examples/project/` for supported-format coverage.
