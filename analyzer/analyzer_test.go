package analyzer

import (
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
