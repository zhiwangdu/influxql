# InfluxQL Analyzer

## Overview

`influxql-analyze` 是一个基于当前仓库 InfluxQL parser 的离线分析工具，用来做两件事：

1. 把原始 InfluxQL 解析并归一化，生成稳定的 `fingerprint`，用于 SQL 归类。
2. 基于执行时间窗口统计“特殊查询”，例如无时间条件、正则查询、通配符查询、大 `LIMIT`、高基数 `GROUP BY`、元数据查询、写入或破坏性查询。

工具由两部分组成：

- 库包：`analyzer/`
- CLI：`cmd/influxql-analyze`


## Usage

### Build

如果本机 `go` 路径存在多个版本，建议显式使用一致的 Go 二进制：

```bash
/Users/duzhiwang/devkits/go125/go/bin/go build ./cmd/influxql-analyze
```

也可以直接运行：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze
```

### Input Format

输入格式是 `JSONL`，每行一条记录，至少包含：

```json
{"timestamp":"2026-04-20T10:00:00Z","query":"SELECT * FROM cpu"}
```

字段说明：

- `timestamp`: 执行时间，默认按 `RFC3339` 解析，也支持 Unix 秒时间戳数字
- `query`: 原始 InfluxQL 语句

额外字段会被读取进 `Raw`，但当前版本不会参与统计。

### Basic Command

从标准输入读取：

```bash
cat input.jsonl | env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze
```

从文件读取：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze \
  -input ./input.jsonl
```

带时间窗口过滤：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze \
  -input ./input.jsonl \
  -window-start 2026-04-20T10:00:00Z \
  -window-end 2026-04-20T11:00:00Z
```

限制每个 bucket 返回的样例数量：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze \
  -input ./input.jsonl \
  -detail-limit 2
```

使用规则配置文件：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze \
  -input ./input.jsonl \
  -config ./config.json
```

### Supported Flags

- `-input`: JSONL 输入文件，缺省时读取 `stdin`
- `-config`: JSON 配置文件
- `-window-start`: 时间窗口起点，`RFC3339`
- `-window-end`: 时间窗口终点，`RFC3339`
- `-output`: 当前仅支持 `json`
- `-detail-limit`: 每个聚合桶保留的样例查询数
- `-workers`: 并发分析 worker 数，默认等于当前 `GOMAXPROCS`


## Mock Input And Output

### Sample Input

```json
{"timestamp":"2026-04-20T10:00:00Z","query":"SELECT * FROM cpu"}
{"timestamp":"2026-04-20T10:01:00Z","query":"SELECT * FROM cpu"}
{"timestamp":"2026-04-20T10:02:00Z","query":"SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ WHERE region =~ /cn/ LIMIT 1000"}
{"timestamp":"2026-04-20T10:03:00Z","query":"SELECT"}
```

### Example Command

```bash
cat <<'EOF' | env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go run ./cmd/influxql-analyze -detail-limit 2
{"timestamp":"2026-04-20T10:00:00Z","query":"SELECT * FROM cpu"}
{"timestamp":"2026-04-20T10:01:00Z","query":"SELECT * FROM cpu"}
{"timestamp":"2026-04-20T10:02:00Z","query":"SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ WHERE region =~ /cn/ LIMIT 1000"}
{"timestamp":"2026-04-20T10:03:00Z","query":"SELECT"}
EOF
```

### Example Output

输出会是格式化后的 JSON，大意如下：

- `total_records = 4`
- `total_statements = 3`
- `parse_error_count = 1`
- `SELECT * FROM cpu` 被聚成一个 fingerprint，`count = 2`
- `SHOW TAG VALUES ... LIMIT 1000` 被归一化成：

```sql
SHOW TAG VALUES FROM cpu WITH KEY =~ /.*/ WHERE region =~ /.*/ LIMIT 1
```

- 命中的规则包括：
  - `has_wildcard`
  - `no_time_filter`
  - `has_regex`
  - `large_limit`
  - `meta_query`


## Output Structure

CLI 默认输出一个 JSON `Report`：

```json
{
  "window_start": "optional",
  "window_end": "optional",
  "total_records": 0,
  "records_in_window": 0,
  "total_statements": 0,
  "parse_error_count": 0,
  "fingerprints": [],
  "special_rules": [],
  "parse_errors": []
}
```

关键字段：

- `fingerprints`: 按归一化后 SQL 聚合
  - `fingerprint`: `normalized_query` 的 SHA-256
  - `statement_type`: 语句类型
  - `normalized_query`: 归一化后的 InfluxQL
  - `count`: 出现次数
  - `sample_queries`: 原始样例
  - `rules`: 该 fingerprint 命中的规则
- `special_rules`: 按规则聚合
  - `rule`: 规则名
  - `count`: 命中次数
  - `fingerprints`: 命中的 fingerprint 列表
- `parse_errors`: 解析失败桶
  - `error`: parser 错误信息
  - `count`: 出现次数
  - `sample_queries`: 失败样例


## Rule Config

配置文件格式是 JSON，对应 `analyzer.AnalyzerConfig`。

示例：

```json
{
  "detail_limit": 5,
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
      "write_or_destructive": true
    }
  }
}
```

当前支持的规则：

- `no_time_filter`
- `has_regex`
- `has_wildcard`
- `large_limit`
- `group_by_high_cardinality_risk`
- `meta_query`
- `write_or_destructive`

默认阈值：

- `large_limit_threshold = 1000`
- `group_by_tag_threshold = 2`


## Internal Structure

### Entry

- [cmd/influxql-analyze/main.go](/Users/duzhiwang/workspace/goWorkspace/influxql/cmd/influxql-analyze/main.go)

职责：

- 解析 CLI 参数
- 读取 JSONL
- 解析时间窗口和配置
- 调用 `analyzer.New(...).AddRecord(...)`
- 输出最终 `Report`

### Core Package

- [analyzer/analyzer.go](/Users/duzhiwang/workspace/goWorkspace/influxql/analyzer/analyzer.go)

职责：

- 定义核心数据结构：`Record`、`NormalizeResult`、`FeatureSet`、`RuleSummary`、`Report`
- 管理聚合状态
- 对外暴露：
  - `DefaultAnalyzerConfig`
  - `New`
  - `Analyze`
  - `NormalizeQuery`
  - `AddRecord`
  - `Report`

### Normalization And Feature Extraction

- [analyzer/normalize.go](/Users/duzhiwang/workspace/goWorkspace/influxql/analyzer/normalize.go)

职责：

- 调用仓库现有 parser：`influxql.ParseQuery`
- 基于 AST 做模板化归一化
- 提取规则匹配依赖的结构特征

当前归一化策略：

- 保留语句结构
- 保留 measurement、tag key、field key、函数名
- 把字符串、时间、数值、duration、regex、list、参数等替换为模板值
- 把 `LIMIT` / `OFFSET` / `SLIMIT` / `SOFFSET` 归一化到固定值

### Rule Matching

- [analyzer/rules.go](/Users/duzhiwang/workspace/goWorkspace/influxql/analyzer/rules.go)

职责：

- 根据 `FeatureSet` 和配置做规则判定
- 支持启用/禁用规则
- 支持阈值配置


## Current Behavior

### What Is Stable

- 多条 statement 会按 parser 结果拆分并分别统计
- 解析失败的语句不会中断整批分析，会进入 `parse_errors`
- 同结构不同字面量的查询会归到同一个 fingerprint
- 输出 JSON 字段结构稳定，适合后续做离线分析或接入其他系统

### Current Limits

- 当前只支持 `JSON` 输出
- 规则扩展目前是“参数化 + 开关”，不是完整 DSL
- 执行时间只用于窗口过滤，不会用于把 `now()` 折算成具体时间
- 归一化是模板化，不会主动抹平 measurement 或字段名差异

### Concurrency

当前版本已经支持并发分析：

- CLI 通过 `-workers` 控制并发 worker 数
- 读取输入仍按流式逐行进行
- 分析阶段按批次并发执行，再合并到最终聚合结果

适合的场景：

- 大量独立查询日志的批量归类
- parser 和归一化是主要 CPU 开销时


## Test Status

### Automated Tests

测试文件：

- [analyzer/analyzer_test.go](/Users/duzhiwang/workspace/goWorkspace/influxql/analyzer/analyzer_test.go)

当前覆盖：

- 不同字面量查询归到同一个 fingerprint
- 同一类查询的规则聚合和计数
- parser 失败语句进入错误桶

### Verified Command

已在当前环境使用固定 Go 二进制跑通：

```bash
env GOCACHE=/tmp/influxql-gocache /Users/duzhiwang/devkits/go125/go/bin/go test ./analyzer
```

结果：

```text
ok  	github.com/influxdata/influxql/analyzer	1.135s
```

### Environment Note

当前机器同时存在多个 Go toolchain 路径。直接使用 `go` 可能会命中不一致的 `go1.25.7` / `go1.25.8` 组合，导致编译报错。当前可工作的方式是显式使用：

```bash
/Users/duzhiwang/devkits/go125/go/bin/go
```


## Suggested Next Steps

- 给 CLI 增加 `detail` 模式，输出逐条命中的明细记录
- 给规则系统补充更细粒度的阈值和白名单能力
- 如果后续需要做服务化，再在 `analyzer` 包之上包一层 HTTP API
