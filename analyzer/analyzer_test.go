package analyzer

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeQueryLiteralsCollapseToSameFingerprint(t *testing.T) {
	first, err := NormalizeQuery(`SELECT mean(value) FROM cpu WHERE host = 'a' AND time >= '2024-01-01T00:00:00Z' LIMIT 10`)
	if err != nil {
		t.Fatalf("normalize first query: %v", err)
	}
	second, err := NormalizeQuery(`select mean(value) from cpu where host = 'b' and time >= '2025-02-01T00:00:00Z' limit 999`)
	if err != nil {
		t.Fatalf("normalize second query: %v", err)
	}

	if got, want := len(first), 1; got != want {
		t.Fatalf("first normalized statements = %d, want %d", got, want)
	}
	if got, want := len(second), 1; got != want {
		t.Fatalf("second normalized statements = %d, want %d", got, want)
	}
	if first[0].Fingerprint != second[0].Fingerprint {
		t.Fatalf("fingerprints differ:\nfirst=%s\nsecond=%s", first[0].Normalized, second[0].Normalized)
	}
	if !strings.Contains(first[0].Normalized, "LIMIT 1") {
		t.Fatalf("normalized query did not collapse limit: %s", first[0].Normalized)
	}
}

func TestAnalyzeAggregatesFingerprintsAndRules(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.Rules.LargeLimitThreshold = 100

	analyzer := New(cfg, nil, nil)
	records := []Record{
		{
			Timestamp: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
			Query:     `SELECT * FROM cpu`,
		},
		{
			Timestamp: time.Date(2026, 4, 20, 10, 1, 0, 0, time.UTC),
			Query:     `SELECT * FROM cpu`,
		},
		{
			Timestamp: time.Date(2026, 4, 20, 10, 2, 0, 0, time.UTC),
			Query:     `SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ WHERE region =~ /cn/ LIMIT 1000`,
		},
	}

	for _, record := range records {
		if err := analyzer.AddRecord(record); err != nil {
			t.Fatalf("add record: %v", err)
		}
	}

	report := analyzer.Report()
	if got, want := report.TotalRecords, 3; got != want {
		t.Fatalf("total records = %d, want %d", got, want)
	}
	if got, want := report.TotalStatements, 3; got != want {
		t.Fatalf("total statements = %d, want %d", got, want)
	}
	if len(report.Fingerprints) != 2 {
		t.Fatalf("fingerprints = %d, want 2", len(report.Fingerprints))
	}
	if report.Fingerprints[0].Count != 2 {
		t.Fatalf("first fingerprint count = %d, want 2", report.Fingerprints[0].Count)
	}

	rules := make(map[string]int)
	for _, rule := range report.SpecialRules {
		rules[rule.Rule] = rule.Count
	}
	if rules[RuleNoTimeFilter] != 2 {
		t.Fatalf("no_time_filter count = %d, want 2", rules[RuleNoTimeFilter])
	}
	if rules[RuleHasRegex] != 1 {
		t.Fatalf("has_regex count = %d, want 1", rules[RuleHasRegex])
	}
	if rules[RuleLargeLimit] != 1 {
		t.Fatalf("large_limit count = %d, want 1", rules[RuleLargeLimit])
	}
	if rules[RuleMetaQuery] != 1 {
		t.Fatalf("meta_query count = %d, want 1", rules[RuleMetaQuery])
	}
}

func TestAnalyzerQueryCacheCanBeDisabled(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.QueryCacheSize = 0
	analyzer := New(cfg, nil, nil)
	if analyzer.queryCache != nil {
		t.Fatal("query cache initialized when query_cache_size is 0")
	}
}

func TestAnalyzerQueryCacheIsBounded(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.QueryCacheSize = 2
	analyzer := New(cfg, nil, nil)
	records := []Record{
		{Timestamp: time.Now().UTC(), Query: `SELECT * FROM cpu`},
		{Timestamp: time.Now().UTC(), Query: `SELECT * FROM mem`},
		{Timestamp: time.Now().UTC(), Query: `SELECT * FROM disk`},
	}
	for _, record := range records {
		if err := analyzer.AddRecord(record); err != nil {
			t.Fatalf("add record: %v", err)
		}
	}
	if got, want := len(analyzer.queryCache), 2; got != want {
		t.Fatalf("query cache size = %d, want %d", got, want)
	}
}

func TestAnalyzeParseErrorsBucketed(t *testing.T) {
	analyzer := New(DefaultAnalyzerConfig(), nil, nil)
	if err := analyzer.AddRecord(Record{
		Timestamp: time.Now().UTC(),
		Query:     `SELECT`,
	}); err != nil {
		t.Fatalf("add invalid record: %v", err)
	}

	report := analyzer.Report()
	if got, want := report.ParseErrorCount, 1; got != want {
		t.Fatalf("parse error count = %d, want %d", got, want)
	}
	if len(report.ParseErrors) != 1 {
		t.Fatalf("parse error buckets = %d, want 1", len(report.ParseErrors))
	}
}

func TestAnalyzeParallelAddRecords(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.Workers = 4
	cfg.Rules.LargeLimitThreshold = 100

	analyzer := New(cfg, nil, nil)
	records := []Record{
		{
			Timestamp: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
			Query:     `SELECT * FROM cpu`,
		},
		{
			Timestamp: time.Date(2026, 4, 20, 10, 1, 0, 0, time.UTC),
			Query:     `SELECT * FROM cpu`,
		},
		{
			Timestamp: time.Date(2026, 4, 20, 10, 2, 0, 0, time.UTC),
			Query:     `SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ WHERE region =~ /cn/ LIMIT 1000`,
		},
		{
			Timestamp: time.Date(2026, 4, 20, 10, 3, 0, 0, time.UTC),
			Query:     `SELECT`,
		},
	}

	if err := analyzer.AddRecords(records); err != nil {
		t.Fatalf("add records: %v", err)
	}

	report := analyzer.Report()
	if got, want := report.TotalRecords, 4; got != want {
		t.Fatalf("total records = %d, want %d", got, want)
	}
	if got, want := report.TotalStatements, 3; got != want {
		t.Fatalf("total statements = %d, want %d", got, want)
	}
	if got, want := report.ParseErrorCount, 1; got != want {
		t.Fatalf("parse error count = %d, want %d", got, want)
	}
	if len(report.Fingerprints) != 2 {
		t.Fatalf("fingerprints = %d, want 2", len(report.Fingerprints))
	}
}

func TestCompareReports(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	aRecords := []Record{
		{Timestamp: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), Query: `SELECT * FROM cpu`},
		{Timestamp: time.Date(2026, 4, 20, 10, 0, 10, 0, time.UTC), Query: `SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ LIMIT 10`},
	}
	bRecords := []Record{
		{Timestamp: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), Query: `SELECT * FROM cpu`},
		{Timestamp: time.Date(2026, 4, 20, 10, 0, 5, 0, time.UTC), Query: `SELECT * FROM cpu`},
		{Timestamp: time.Date(2026, 4, 20, 10, 0, 10, 0, time.UTC), Query: `SELECT mean(value) FROM cpu WHERE time >= '2026-04-20T10:00:00Z'`},
	}

	aReport, err := Analyze(aRecords, cfg, nil, nil)
	if err != nil {
		t.Fatalf("analyze A: %v", err)
	}
	bReport, err := Analyze(bRecords, cfg, nil, nil)
	if err != nil {
		t.Fatalf("analyze B: %v", err)
	}

	diff := CompareReports("A", *aReport, "B", *bReport)
	if got, want := diff.StatementDelta, 1; got != want {
		t.Fatalf("statement delta = %d, want %d", got, want)
	}
	if len(diff.NewFingerprints) != 1 {
		t.Fatalf("new fingerprints = %d, want 1", len(diff.NewFingerprints))
	}
	if len(diff.RemovedFingerprints) != 1 {
		t.Fatalf("removed fingerprints = %d, want 1", len(diff.RemovedFingerprints))
	}
	if len(diff.ChangedFingerprints) != 1 {
		t.Fatalf("changed fingerprints = %d, want 1", len(diff.ChangedFingerprints))
	}
	if diff.ChangedFingerprints[0].CountDelta != 1 {
		t.Fatalf("changed fingerprint count delta = %d, want 1", diff.ChangedFingerprints[0].CountDelta)
	}
	if diff.BatchA.QPS <= 0 || diff.BatchB.QPS <= 0 {
		t.Fatalf("expected positive QPS, got A=%f B=%f", diff.BatchA.QPS, diff.BatchB.QPS)
	}
}

func TestAnalyzeRealtimeQuerySummary(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.RealtimeQuery.ThresholdSeconds = int64((2 * time.Hour).Seconds())
	base := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	ms := base.Add(-30*time.Minute).UnixNano() / int64(time.Millisecond)
	records := []Record{
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= '2026-06-08 00:00:00'`},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= '2026-06-08T11:00:00Z'`},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= now() - 30m`},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= ` + strconv.FormatInt(ms, 10)},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= '2026-06-01T00:00:00Z'`},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time < '2026-06-08T12:00:00Z'`},
		{Timestamp: base, Query: `SELECT * FROM cpu`},
	}

	report, err := Analyze(records, cfg, nil, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if got, want := report.RealtimeQuery.Total, 7; got != want {
		t.Fatalf("realtime total = %d, want %d", got, want)
	}
	if got, want := report.RealtimeQuery.Realtime, 4; got != want {
		t.Fatalf("realtime count = %d, want %d", got, want)
	}
	if got, want := report.RealtimeQuery.NonRealtime, 2; got != want {
		t.Fatalf("non realtime count = %d, want %d", got, want)
	}
	if got, want := report.RealtimeQuery.Unknown, 1; got != want {
		t.Fatalf("unknown count = %d, want %d", got, want)
	}
	if report.RealtimeQuery.AllRealtime {
		t.Fatal("all_realtime = true, want false")
	}

	rules := make(map[string]int)
	for _, rule := range report.SpecialRules {
		rules[rule.Rule] = rule.Count
	}
	if got, want := rules[RuleNotRealtimeQuery], 2; got != want {
		t.Fatalf("not realtime rule count = %d, want %d", got, want)
	}
}

func TestAnalyzeRealtimeQuerySummaryParallelMerge(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.Workers = 2
	base := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	records := []Record{
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= now() - 5m`},
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= '2026-06-01T00:00:00Z'`},
	}

	report, err := Analyze(records, cfg, nil, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if got, want := report.RealtimeQuery.Total, 2; got != want {
		t.Fatalf("realtime total = %d, want %d", got, want)
	}
	if got, want := report.RealtimeQuery.Realtime, 1; got != want {
		t.Fatalf("realtime count = %d, want %d", got, want)
	}
	if got, want := report.RealtimeQuery.NonRealtime, 1; got != want {
		t.Fatalf("non realtime count = %d, want %d", got, want)
	}
}

func TestAnalyzeRealtimeNonRealtimeLogTimeDistribution(t *testing.T) {
	cfg := DefaultAnalyzerConfig()
	cfg.Workers = 2
	base := time.Date(2026, 6, 8, 12, 15, 0, 0, time.UTC)
	records := []Record{
		{Timestamp: base, Query: `SELECT * FROM cpu WHERE time >= '2026-06-01T00:00:00Z'`},
		{Timestamp: base.Add(20 * time.Minute), Query: `SELECT * FROM cpu WHERE time >= '2026-06-01T00:00:00Z'`},
		{Timestamp: base.Add(time.Hour), Query: `SELECT * FROM cpu WHERE time >= '2026-06-01T00:00:00Z'`},
		{Timestamp: base.Add(time.Hour), Query: `SELECT * FROM cpu WHERE time >= now() - 5m`},
	}

	report, err := Analyze(records, cfg, nil, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	buckets := report.RealtimeQuery.NonRealtimeLogTimeDistribution
	if got, want := len(buckets), 2; got != want {
		t.Fatalf("bucket count = %d, want %d: %#v", got, want, buckets)
	}
	if got, want := buckets[0].BucketStart, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("first bucket start = %s, want %s", got, want)
	}
	if got, want := buckets[0].Count, 2; got != want {
		t.Fatalf("first bucket count = %d, want %d", got, want)
	}
	if got, want := buckets[1].BucketStart, time.Date(2026, 6, 8, 13, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("second bucket start = %s, want %s", got, want)
	}
	if got, want := buckets[1].Count, 1; got != want {
		t.Fatalf("second bucket count = %d, want %d", got, want)
	}
}
