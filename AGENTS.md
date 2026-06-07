# Repository Guidelines

## Project Structure & Module Organization

This repository is the Go module `github.com/influxdata/influxql`. Core parser, scanner, AST, token, and sanitization code lives at the repository root in files such as `parser.go`, `scanner.go`, `ast.go`, and `sanitize.go`. Unit tests sit beside the code with `_test.go` suffixes. The query analyzer library is in `analyzer/`, with its command-line entry point in `cmd/influxql-analyze/`. Generated protobuf support files live in `internal/`; avoid hand-editing generated `.pb.go` output. User-facing analyzer documentation is in `docs/influxql-analyzer.md`.

## Build, Test, and Development Commands

- `go test ./...`: runs all package tests; use this as the main local check.
- `go vet ./...`: runs static checks; CircleCI runs this before tests.
- `go test -v`: runs root package tests verbosely, matching the Jenkins test stage.
- `go build ./cmd/influxql-analyze`: builds the analyzer CLI.
- `go run ./cmd/influxql-analyze -input ./input.jsonl`: runs the analyzer locally against JSONL input.

Use Go 1.18 compatibility unless `go.mod` and CI images are updated.

## Coding Style & Naming Conventions

Format Go code with `gofmt` or `go fmt ./...`; use tabs as produced by the formatter. Keep package names short and lowercase, matching `influxql` and `analyzer`. Exported identifiers should have clear Go doc comments when they are public API. Follow existing AST and parser naming patterns, for example `FooStatement`, `FooExpr` and all-caps token constants.

## Testing Guidelines

Tests use Go's standard `testing` package. Place tests next to the code they exercise and name files `*_test.go`. Prefer table-driven tests for parser, scanner, and analyzer behavior, with both accepted inputs and error cases for syntax changes. Parser and formatter changes usually belong in `parser_test.go`, `scanner_test.go`, `ast_test.go`, or `sanitize_test.go`; analyzer changes should include `analyzer/analyzer_test.go`.

## Commit & Pull Request Guidelines

Recent history uses concise imperative subjects, sometimes with conventional prefixes, such as `Add CLI progress reporting`, `fix: Adds a nil check...`, and `feat: add FUTURE LIMIT...`. Keep commits focused on one behavior. Pull requests should include a short summary, tests run (`go test ./...`, `go vet ./...`), linked issues when relevant, and CLI examples or documentation updates for user-facing analyzer changes.

## Security & Configuration Tips

Do not commit production query logs or credentials. Analyzer inputs should be local JSONL fixtures with sanitized queries. Keep generated files reproducible from source definitions and call out any regeneration command when protobuf files change.
