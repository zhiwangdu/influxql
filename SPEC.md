# InfluxQL Analyzer Specification

This file is the primary behavior specification for the current `influxql-analyze` tool. Agents should use it as the product contract before changing analyzer code.

## Purpose

`influxql-analyze` is an offline analyzer for InfluxQL query logs. It uses this repository's InfluxQL parser to:

1. Parse raw InfluxQL queries.
2. Normalize statements into stable query fingerprints.
3. Aggregate query volume by fingerprint.
4. Detect special query patterns.
5. Classify whether select-like queries are realtime based on `WHERE time` and log time.
6. Compare two query batches for fingerprint, rule, count, and QPS deltas.

## Packages

- Library package: `analyzer`
- CLI package: `cmd/influxql-analyze`

The CLI is a thin input/output wrapper around the library. Core behavior should generally live in `analyzer/`.

## Input

Input is JSONL, one record per line. Empty lines are skipped. A line may be either a pure JSON object or a larger log line containing one JSON object.

Minimum useful fields:

```json
{"timestamp":"2026-04-20T10:00:00Z","query":"SELECT * FROM cpu"}
```

Fields:

- `query`: required string. Raw InfluxQL query text.
- `timestamp`: optional execution time. Takes precedence over `time`.
  - String values parse as RFC3339/RFC3339Nano.
  - Numeric values parse as Unix seconds.
- `time`: optional log time string used only when `timestamp` is absent. Parses as RFC3339/RFC3339Nano.

Current CLI decoding extracts only `query`, `timestamp`, and `time`; it does not retain the full raw log map.

## CLI Flags

- `-input`: JSONL input file. Defaults to stdin.
- `-input-a`: baseline JSONL input for compare mode.
- `-input-b`: candidate JSONL input for compare mode.
- `-config`: JSON analyzer config file.
- `-window-start`: RFC3339 lower bound for record timestamp filtering.
- `-window-end`: RFC3339 upper bound for record timestamp filtering.
- `-output`: output format. Only `json` is supported.
- `-detail-limit`: max sample queries per aggregation bucket. Default `3`.
- `-workers`: concurrent analyzer workers. Default `runtime.GOMAXPROCS(0)`.
- `-progress-every`: stderr progress interval in input records. Default `10000`.
- `-query-cache-size`: max query cache entries. Default `10000`; `0` disables cache.
- `-realtime-threshold`: max age for realtime lower bound using Go duration syntax, for example `2h` or `30m`. Default `24h`.

Compare mode requires both `-input-a` and `-input-b`.

## Analyzer Config

`AnalyzerConfig`:

```json
{
  "detail_limit": 3,
  "workers": 4,
  "query_cache_size": 10000,
  "rules": {
    "enabled": {
      "no_time_filter": true,
      "not_realtime_query": true
    },
    "large_limit_threshold": 1000,
    "group_by_tag_threshold": 2
  },
  "realtime_query": {
    "threshold_seconds": 86400
  }
}
```

Defaults:

- `detail_limit`: `3`
- `query_cache_size`: `10000`
- `rules.large_limit_threshold`: `1000`
- `rules.group_by_tag_threshold`: `2`
- `realtime_query.threshold_seconds`: `86400`

When `rules.enabled` is omitted, all rules are enabled. When a rule key is absent from `rules.enabled`, that rule is enabled.

## Output: Report

Normal mode emits a JSON `Report`:

```json
{
  "window_start": "optional RFC3339",
  "window_end": "optional RFC3339",
  "observed_start": "optional RFC3339",
  "observed_end": "optional RFC3339",
  "total_records": 0,
  "records_in_window": 0,
  "total_statements": 0,
  "parse_error_count": 0,
  "fingerprints": [],
  "special_rules": [],
  "parse_errors": [],
  "realtime_query": {}
}
```

Top-level counters:

- `total_records`: all decoded non-empty input records.
- `records_in_window`: records included after optional timestamp filtering.
- `total_statements`: parsed statements in included records.
- `parse_error_count`: records whose query failed to parse.
- `observed_start` / `observed_end`: min/max included record timestamp, excluding zero timestamps.

## Fingerprints

Each fingerprint bucket contains:

- `fingerprint`: SHA-256 of normalized query text.
- `statement_type`: statement type such as `SELECT`, `SHOW TAG VALUES`, or `DELETE`.
- `normalized_query`: normalized InfluxQL.
- `count`: statement occurrence count.
- `sample_queries`: raw query samples, capped by `detail_limit`.
- `rules`: special rules hit by this fingerprint.

Normalization replaces volatile literals with stable placeholders while preserving query shape. Examples:

- string literals become `'str'`, except time-like strings normalize to `'1970-01-01T00:00:00Z'`
- numbers become `0` / `0.0`
- limits and offsets become `1`
- regex literals become `/.*/`
- tag-key list literals become `(tag)`

## Special Rules

Current special rules:

- `no_time_filter`: `SELECT` statement has no explicit time predicate.
- `has_regex`: query uses regex matching or regex measurement/source.
- `has_wildcard`: query uses wildcard selection, grouping, or supported wildcard metadata scope.
- `large_limit`: `LIMIT` or `SLIMIT` is greater than or equal to `large_limit_threshold`.
- `group_by_high_cardinality_risk`: non-time `GROUP BY` dimensions are greater than or equal to `group_by_tag_threshold`.
- `meta_query`: metadata or explain statement.
- `write_or_destructive`: statement writes data or performs destructive changes, including `SELECT INTO`.
- `not_realtime_query`: select-like statement has an extractable `WHERE time` predicate and is explicitly non-realtime.

Rule summaries contain:

- `rule`
- `count`
- `fingerprints`

## Realtime Query Classification

Realtime classification applies to select-like statements:

- `SELECT`
- `EXPLAIN SELECT`
- `CREATE CONTINUOUS QUERY` source select

The analyzer extracts `WHERE time` comparisons from the statement condition and compares the resulting time range with the record log time.

### Supported Time Predicate Forms

Supported comparison operators:

- `time > value`
- `time >= value`
- `time < value`
- `time <= value`
- `time = value`
- reversed forms such as `value <= time`

Supported expression structure:

- `AND`-combined predicates are intersected.
- Parentheses are unwrapped.
- `OR` involving time predicates is unsupported and classified as unknown.

Supported value forms:

- RFC3339/RFC3339Nano string, for example `'2026-06-08T11:00:00Z'`
- InfluxQL datetime string, for example `'2026-06-08 11:00:00'`
- date-only string, for example `'2026-06-08'`
- `now()` expressions reducible by `influxql.NowValuer`, for example `now() - 30m`
- numeric timestamps inferred by magnitude:
  - `>= 1e17`: nanoseconds
  - `>= 1e14`: microseconds
  - `>= 1e11`: milliseconds
  - otherwise: seconds

Numeric timestamp inference is analyzer-local and does not change the core InfluxQL parser's numeric time semantics.

### Statuses

`realtime`:

- extracted time range has a lower bound, and
- lower bound is not after log time, and
- lower bound is on the same local day as log time, or lower bound age is within `realtime_query.threshold_seconds`

`non_realtime`:

- an extractable time predicate exists, but it has no lower bound; or
- lower bound is after log time; or
- lower bound is older than the configured threshold and not on the same local day

`unknown`:

- log time is empty;
- no `WHERE time` predicate exists;
- no extractable time predicate exists;
- time predicate uses unsupported `OR`;
- time value cannot be parsed or reduced.

`all_realtime` is true only when `total > 0`, `non_realtime == 0`, and `unknown == 0`.

Realtime summary fields:

- `threshold_seconds`
- `total`
- `realtime`
- `non_realtime`
- `unknown`
- `all_realtime`
- `sample_non_realtime`
- `sample_unknown`
- `non_realtime_log_time_distribution`: UTC hourly buckets for non-realtime query log times. Each bucket has `bucket_start`, `bucket_end`, and `count`.

Samples include:

- `query`
- `reason`
- `log_time`
- `time_range.min`
- `time_range.max`
- `time_range.window_seconds` when both bounds exist

## Parse Errors

Parse errors are bucketed by parser error string. Each bucket contains:

- `error`
- `count`
- `sample_queries`

Parse errors do not stop batch analysis.

## Compare Mode

Compare mode emits a JSON `CompareReport`:

```json
{
  "batch_a": {},
  "batch_b": {},
  "statement_delta": 0,
  "qps_delta": 0,
  "new_fingerprints": [],
  "removed_fingerprints": [],
  "changed_fingerprints": [],
  "rule_deltas": []
}
```

Batch stats include totals, parse errors, observed/window time bounds, effective duration, and QPS.

Effective duration:

1. Use `window_end - window_start` when both are present and ordered.
2. Otherwise use `observed_end - observed_start` when positive.
3. Otherwise use `1` second when there are statements or records.
4. Otherwise use `0`.

Fingerprint deltas classify fingerprints as:

- `added`
- `removed`
- `changed`

Rule deltas compare special rule counts and QPS.

## Caching

The analyzer query cache is keyed by raw query string. Cached entries include:

- normalized statement results
- realtime checks cloned before normalization

This is important because realtime classification depends on the per-record log timestamp, while parsing and static AST extraction can be reused.

`query_cache_size = 0` disables the cache.

## Concurrency

`Analyzer.AddRecords` can split input records across worker-local analyzers when `Workers > 1`. Partial analyzers are merged into the parent report.

Merge behavior must preserve:

- total counters
- observed timestamp bounds
- fingerprint buckets and samples
- special rule buckets
- parse error buckets
- realtime query counters and samples

## Progress Output

CLI progress is printed to stderr only. JSON reports are written to stdout.

File input performs a first pass to count records. Stdin cannot know total count in advance.

## Validation Commands

Use these commands for normal validation:

```bash
env GOCACHE=/tmp/influxql-gocache go test ./...
env GOCACHE=/tmp/influxql-gocache go vet ./...
```

For analyzer performance changes:

```bash
env GOCACHE=/tmp/influxql-gocache go test ./analyzer ./cmd/influxql-analyze -run=BenchmarkNeverMatches -bench=Throughput -benchmem
```

## Non-Goals And Constraints

- Do not hand-edit generated protobuf output in `internal/`.
- Do not store production logs, credentials, or sensitive query samples in the repository.
- Do not implement analyzer behavior by ad hoc SQL string parsing when the AST can provide the information.
- Do not change core parser semantics to satisfy analyzer-only reporting needs unless the parser behavior itself is intentionally being changed.
