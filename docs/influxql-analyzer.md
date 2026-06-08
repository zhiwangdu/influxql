# InfluxQL Analyzer

## 概览

`influxql-analyze` 是一个基于当前仓库 InfluxQL parser 的离线查询日志分析工具。当前功能包括：

1. 解析原始 InfluxQL，并归一化成稳定的 `fingerprint`。
2. 按 fingerprint 聚合查询量和样例。
3. 识别特殊查询：无时间条件、正则、通配符、大 `LIMIT`、高基数 `GROUP BY`、元数据查询、写入或破坏性查询、非实时查询。
4. 根据 SQL 中的 `WHERE time` 和日志时间，判断 select-like 查询是实时、非实时还是未知。
5. 对比两批查询，输出 fingerprint、规则、次数和 QPS 的变化。

代码分为两部分：

- 库包：`analyzer/`
- CLI：`cmd/influxql-analyze/`

更紧凑的行为规格见仓库根目录 `SPEC.md`。

## 构建和运行

构建 CLI：

```bash
go build ./cmd/influxql-analyze
```

从标准输入读取：

```bash
cat input.jsonl | go run ./cmd/influxql-analyze
```

从文件读取：

```bash
go run ./cmd/influxql-analyze -input ./input.jsonl
```

在受限环境中建议显式使用可写 Go cache：

```bash
env GOCACHE=/tmp/influxql-gocache go run ./cmd/influxql-analyze -input ./input.jsonl
```

## 输入格式

输入是 JSONL，每行一条记录。非空行必须包含一个 JSON 对象；这个 JSON 对象可以是整行，也可以嵌在更大的文本日志行里。

最小可用记录：

```json
{"timestamp":"2026-04-20T10:00:00Z","query":"SELECT * FROM cpu"}
```

字段说明：

- `query`: 必填，原始 InfluxQL 查询字符串。
- `timestamp`: 可选，执行时间；存在时优先于 `time`。
  - 字符串按 RFC3339/RFC3339Nano 解析。
  - 数字按 Unix 秒解析。
- `time`: 可选日志时间字符串；仅在缺少 `timestamp` 时使用，按 RFC3339/RFC3339Nano 解析。

executor 日志行示例：

```json
{"level":"info","time":"2026-06-06T16:10:12.675546+08:00","msg":"Executing query","hostname":"127.0.0.1:8086","service":"executor","query":"SELECT * FROM mydb.autogen.cpu","batch":1,"location":"query/executor.go:535","repeated":1}
```

当前 CLI 解码路径只提取 `query`、`timestamp` 和 `time`，不会把完整日志对象保存在 `Record.Raw` 中。

## CLI 参数

- `-input`: JSONL 输入文件，缺省时读取 stdin。
- `-input-a`: 对比模式中的基线 JSONL 输入文件。
- `-input-b`: 对比模式中的候选 JSONL 输入文件。
- `-config`: JSON 配置文件，对应 `analyzer.AnalyzerConfig`。
- `-window-start`: 记录时间窗口起点，RFC3339。
- `-window-end`: 记录时间窗口终点，RFC3339。
- `-output`: 输出格式，当前仅支持 `json`。
- `-detail-limit`: 每个聚合桶最多保留的样例查询数，默认 `3`。
- `-workers`: analyzer worker 数，默认 `runtime.GOMAXPROCS(0)`。
- `-progress-every`: 每处理 N 条输入记录向 stderr 打印一次进度，默认 `10000`。
- `-query-cache-size`: 查询缓存最大条目数，默认 `10000`；设置为 `0` 可关闭缓存。
- `-realtime-threshold`: 实时查询下界距离日志时间的最大阈值，使用 Go duration 语法，例如 `2h` 或 `30m`，默认 `24h`。

对比模式必须同时提供 `-input-a` 和 `-input-b`。

## 常用命令

分析一个文件：

```bash
env GOCACHE=/tmp/influxql-gocache go run ./cmd/influxql-analyze \
  -input ./input.jsonl
```

使用记录时间窗口过滤：

```bash
env GOCACHE=/tmp/influxql-gocache go run ./cmd/influxql-analyze \
  -input ./input.jsonl \
  -window-start 2026-04-20T10:00:00Z \
  -window-end 2026-04-20T11:00:00Z
```

调整样例数量和实时查询阈值：

```bash
env GOCACHE=/tmp/influxql-gocache go run ./cmd/influxql-analyze \
  -input ./input.jsonl \
  -detail-limit 5 \
  -realtime-threshold 2h
```

对比两批输入：

```bash
env GOCACHE=/tmp/influxql-gocache go run ./cmd/influxql-analyze \
  -input-a ./baseline.jsonl \
  -input-b ./candidate.jsonl \
  -progress-every 5000 \
  -workers 8
```

## 配置文件

配置文件是 JSON，对应 `analyzer.AnalyzerConfig`。

示例：

```json
{
  "detail_limit": 5,
  "workers": 4,
  "query_cache_size": 10000,
  "rules": {
    "large_limit_threshold": 500,
    "group_by_tag_threshold": 3,
    "enabled": {
      "no_time_filter": true,
      "has_regex": true,
      "has_wildcard": true,
      "large_limit": true,
      "group_by_high_cardinality_risk": true,
      "meta_query": true,
      "write_or_destructive": true,
      "not_realtime_query": true
    }
  },
  "realtime_query": {
    "threshold_seconds": 86400
  }
}
```

默认值：

- `detail_limit = 3`
- `query_cache_size = 10000`
- `large_limit_threshold = 1000`
- `group_by_tag_threshold = 2`
- `realtime_query.threshold_seconds = 86400`

当 `rules.enabled` 省略时，所有规则默认启用。当 `rules.enabled` 存在但缺少某个规则 key 时，该规则也默认启用。

## 实时查询判定

实时查询分析适用于 select-like 语句：

- `SELECT`
- `EXPLAIN SELECT`
- `CREATE CONTINUOUS QUERY` 的 source select

Analyzer 会提取 `WHERE time` 比较条件，并把得到的时间范围和记录日志时间比较。

查询被判定为 `realtime` 需要存在可提取的时间下界，且下界不晚于日志时间，并满足以下任一条件：

- 下界和日志时间在同一个本地日期；
- 下界距离日志时间不超过 `realtime_query.threshold_seconds`。

查询被判定为 `non_realtime` 的情况：

- 存在可提取的时间条件，但没有时间下界；
- 时间下界晚于日志时间；
- 时间下界早于配置阈值，且不在日志时间同一天。

查询被判定为 `unknown` 的情况：

- 日志时间为空；
- 没有 `WHERE time` 条件；
- 没有可提取的时间条件；
- 时间条件位于 `OR` 下；
- 时间值无法解析或无法规约。

支持的 `WHERE time` 值形式：

- RFC3339/RFC3339Nano 字符串，例如 `'2026-06-08T11:00:00Z'`
- InfluxQL datetime 字符串，例如 `'2026-06-08 11:00:00'`
- date-only 字符串，例如 `'2026-06-08'`
- 相对 `now()` 表达式，例如 `now() - 30m`
- 数字时间戳，会按数量级推断为秒、毫秒、微秒或纳秒

数字时间戳推断只存在于 analyzer 的实时查询分析中，不改变核心 InfluxQL parser 的语义。

非实时查询还会命中特殊规则 `not_realtime_query`。

## 输出结构

普通模式输出 JSON `Report`：

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

关键字段：

- `fingerprints`: 按归一化 SQL fingerprint 聚合。
  - `fingerprint`: `normalized_query` 的 SHA-256。
  - `statement_type`: 语句类型。
  - `normalized_query`: 归一化后的 InfluxQL。
  - `count`: 出现次数。
  - `sample_queries`: 原始样例，数量受 `detail_limit` 限制。
  - `rules`: 该 fingerprint 命中的规则。
- `special_rules`: 按规则聚合。
  - `rule`: 规则名。
  - `count`: 命中次数。
  - `fingerprints`: 命中的 fingerprint 列表。
- `realtime_query`: 实时查询汇总。
  - `threshold_seconds`: 当前判定阈值。
  - `total`: 参与实时性分析的 select-like 语句数。
  - `realtime`: 实时查询数量。
  - `non_realtime`: 明确非实时查询数量。
  - `unknown`: 缺少或不支持实时性判定的数量。
  - `all_realtime`: 仅当 `total > 0`、`non_realtime == 0` 且 `unknown == 0` 时为 `true`。
  - `sample_non_realtime` / `sample_unknown`: 样例查询、原因、日志时间和提取到的时间范围。
  - `non_realtime_log_time_distribution`: 非实时查询的日志时间分布，按 UTC 小时分桶；每个桶包含 `bucket_start`、`bucket_end` 和 `count`。
- `parse_errors`: parser 错误桶。
  - `error`: parser 错误文本。
  - `count`: 出现次数。
  - `sample_queries`: 原始样例，数量受 `detail_limit` 限制。

## 对比输出结构

对比模式输出 JSON `CompareReport`：

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

关键字段：

- `batch_a` / `batch_b`: 两批输入各自的总量、解析错误数、观测/窗口时间范围、有效时长和 QPS。
- `statement_delta`: B 相对 A 的 statement 总数变化。
- `qps_delta`: B 相对 A 的整体 QPS 变化。
- `new_fingerprints`: 只在 B 中出现的 fingerprint。
- `removed_fingerprints`: 只在 A 中出现的 fingerprint。
- `changed_fingerprints`: A 和 B 都存在但次数发生变化的 fingerprint。
- `rule_deltas`: 特殊规则命中数和 QPS 的变化。

QPS 有效时长计算顺序：

1. 如果显式提供了有序的 `window-start` / `window-end`，使用窗口时长；
2. 否则使用观测到的 timestamp 最小值和最大值，前提是时长为正；
3. 否则当有数据时退化为 `1` 秒；
4. 否则为 `0`。

## 特殊规则

当前规则：

- `no_time_filter`
- `has_regex`
- `has_wildcard`
- `large_limit`
- `group_by_high_cardinality_risk`
- `meta_query`
- `write_or_destructive`
- `not_realtime_query`

## 内部结构

主要入口：

- `cmd/influxql-analyze/main.go`: CLI 参数、配置加载、JSONL 读取、时间解析、进度输出和报告编码。
- `analyzer/analyzer.go`: public analyzer 类型、聚合状态、记录摄入、查询缓存、报告构造和对比报告。
- `analyzer/normalize.go`: AST 特征提取和 fingerprint 归一化。
- `analyzer/rules.go`: 静态特殊规则匹配。
- `analyzer/realtime.go`: 实时查询提取、判定、样例汇总和 worker 合并 helper。

## 当前行为和限制

稳定行为：

- 多 statement 查询会由 parser 拆分，并按 statement 分别统计。
- 解析失败不会中断整批分析，会聚合到 `parse_errors`。
- 结构相同但字面量不同的查询会归到同一个 fingerprint。
- CLI 进度输出写入 stderr，不污染 stdout 中的 JSON。
- Analyzer 查询缓存以原始 query 为 key，同时保存归一化结果和实时查询检查信息。
- `Workers > 1` 时使用 worker-local analyzer，最后合并状态。

当前限制：

- 输出格式仅支持 JSON。
- 规则配置是阈值和启用/禁用形式，不是完整规则 DSL。
- 实时查询提取支持 `AND` 组合的时间比较，不支持 `OR` 下的时间条件。
- 归一化会保留 measurement 名、field 名、tag key 和函数名。

## Benchmark

运行吞吐量 benchmark：

```bash
env GOCACHE=/tmp/influxql-gocache go test ./analyzer ./cmd/influxql-analyze -run=BenchmarkNeverMatches -bench=Throughput -benchmem
```

benchmark 覆盖 analyzer 批量分析吞吐和 CLI executor 日志解码吞吐。输出中的 `records/s` 是每秒处理记录数。

## 验证命令

运行全量测试：

```bash
env GOCACHE=/tmp/influxql-gocache go test ./...
```

运行 vet：

```bash
env GOCACHE=/tmp/influxql-gocache go vet ./...
```
