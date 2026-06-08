# Repository Agent Guide

This file is the first entry point for agents working in this repository. Read it before editing code, then use `SPEC.md` for the current `influxql-analyze` behavior contract.

## Project Shape

This repository is the Go module `github.com/influxdata/influxql`.

- Core InfluxQL parser, scanner, AST, token, and sanitization code lives at the repository root, especially `parser.go`, `scanner.go`, `ast.go`, `token.go`, and `sanitize.go`.
- Analyzer library code lives in `analyzer/`.
- Analyzer CLI code lives in `cmd/influxql-analyze/`.
- User-facing analyzer documentation lives in `docs/influxql-analyzer.md`.
- Generated protobuf files live in `internal/`; do not hand-edit generated `.pb.go` output.
- `SPEC.md` is the compact product and implementation spec for the analyzer tool.

## Analyzer Entry Points

Use these files first when changing analyzer behavior:

- `analyzer/analyzer.go`: public analyzer types, report structures, aggregation, fingerprint buckets, rule buckets, compare reports, query cache, and record ingestion.
- `analyzer/normalize.go`: feature extraction and AST normalization used for stable query fingerprints.
- `analyzer/rules.go`: special rule matching.
- `analyzer/realtime.go`: realtime-query extraction and classification from `WHERE time` predicates against record log time.
- `cmd/influxql-analyze/main.go`: CLI flags, input decoding, JSONL scanning, progress output, and compare-mode dispatch.
- `analyzer/analyzer_test.go`: analyzer behavior tests.
- `cmd/influxql-analyze/main_test.go`: CLI input decoding tests.

## Current Analyzer Data Flow

1. CLI reads JSONL records from `-input`, stdin, or compare-mode inputs.
2. `decodeRecord` extracts `query` and execution time:
   - `timestamp` wins when present.
   - `time` is used when `timestamp` is absent.
   - string times are parsed as RFC3339/RFC3339Nano.
   - numeric `timestamp` values are Unix seconds.
3. `Analyzer.AddRecord` applies optional record time-window filtering.
4. Query text is parsed through the shared query cache.
5. Each statement produces:
   - `NormalizeResult` for statement type, normalized SQL, fingerprint, and static features.
   - `realtimeCheck` for select-like statement realtime evaluation using the per-record timestamp.
6. Reports aggregate fingerprint counts, special rules, parse errors, observed time bounds, and realtime-query summary.

## Realtime Query Analysis

Realtime analysis is implemented in `analyzer/realtime.go`.

- It applies to select-like statements:
  - `SELECT`
  - `EXPLAIN SELECT`
  - `CREATE CONTINUOUS QUERY` source select
- It compares the record log time with the extracted `WHERE time` range.
- Default threshold is `24h`, configured as `AnalyzerConfig.RealtimeQuery.ThresholdSeconds`.
- CLI override is `-realtime-threshold`, using Go duration syntax such as `2h` or `30m`.
- A query is realtime when the extracted lower bound is on the same local day as the log time, or no older than the configured threshold.
- A query is non-realtime when it has a time predicate but no lower bound, the lower bound is after the log time, or the lower bound is older than the threshold.
- A query is unknown when log time is missing, no extractable `WHERE time` predicate exists, or the predicate uses unsupported forms such as `OR` around time predicates.
- Non-realtime queries also hit special rule `not_realtime_query`. Their log times are additionally aggregated into UTC hourly buckets in `realtime_query.non_realtime_log_time_distribution`.

Supported `WHERE time` literal forms for realtime analysis:

- RFC3339/RFC3339Nano strings, for example `'2026-06-08T11:00:00Z'`
- InfluxQL datetime strings, for example `'2026-06-08 11:00:00'`
- date-only strings, for example `'2026-06-08'`
- relative expressions reducible with `now()`, for example `now() - 30m`
- numeric timestamps inferred as seconds, milliseconds, microseconds, or nanoseconds by magnitude

The realtime analyzer intentionally does not change parser semantics. Numeric timestamp scale inference is analyzer-local.

## Build, Test, And Development Commands

- `go test ./...`: run all package tests; use this as the main local check.
- `go vet ./...`: run static checks; CircleCI runs vet before tests.
- `go test -v`: run root package tests verbosely, matching the Jenkins test stage.
- `go build ./cmd/influxql-analyze`: build the analyzer CLI.
- `go run ./cmd/influxql-analyze -input ./input.jsonl`: run the analyzer locally against JSONL input.
- `env GOCACHE=/tmp/influxql-gocache go test ./...`: preferred in constrained environments where the default Go cache is not writable or stable.

Use Go 1.18 compatibility unless `go.mod` and CI images are explicitly updated.

## Coding Style

- Format Go code with `gofmt` or `go fmt ./...`; use tabs as produced by the formatter.
- Keep package names short and lowercase, matching `influxql` and `analyzer`.
- Exported identifiers should have clear Go doc comments when they are public API.
- Follow existing AST and parser naming patterns, for example `FooStatement`, `FooExpr`, and all-caps token constants.
- Prefer structured AST traversal and parser APIs over string matching for query behavior.
- Keep analyzer changes scoped: normalization, rules, realtime analysis, CLI decoding, and docs each have separate files.

## Testing Guidelines

- Tests use Go's standard `testing` package.
- Place tests next to code they exercise and name files `*_test.go`.
- Prefer table-driven tests for parser, scanner, analyzer, and CLI behavior.
- Parser and formatter changes usually belong in `parser_test.go`, `scanner_test.go`, `ast_test.go`, or `sanitize_test.go`.
- Analyzer feature changes should include `analyzer/analyzer_test.go`.
- CLI input changes should include `cmd/influxql-analyze/main_test.go`.
- Realtime-query changes should test:
  - RFC3339/RFC3339Nano strings
  - `yyyy-mm-dd hh:MM:ss` strings
  - `now() - duration`
  - numeric timestamp scale inference
  - missing lower bound
  - unknown / unsupported predicates
  - worker merge behavior when `Workers > 1`

## Commit And Pull Request Guidelines

Recent history uses concise imperative subjects, sometimes with conventional prefixes, such as `Add CLI progress reporting`, `fix: Adds a nil check...`, and `feat: add FUTURE LIMIT...`.

- Keep commits focused on one behavior.
- Pull requests should include a short summary, tests run (`go test ./...`, `go vet ./...`), linked issues when relevant, and CLI examples or documentation updates for user-facing analyzer changes.
- Do not push production query logs or credentials.

## Security And Data Handling

- Do not commit production query logs, customer data, tokens, or credentials.
- Analyzer fixtures should use local JSONL samples with sanitized queries.
- The optimized CLI decoding path intentionally does not retain full raw log records in `Record.Raw`.
- Keep generated files reproducible from source definitions and call out any regeneration command when protobuf files change.
