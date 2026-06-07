package analyzer

import (
	"testing"
	"time"
)

const benchmarkRecordCount = 8192

func BenchmarkAddRecordsThroughput(b *testing.B) {
	records := makeBenchmarkRecords(benchmarkRecordCount)
	cfg := DefaultAnalyzerConfig()
	cfg.Workers = 1

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	total := 0
	for i := 0; i < b.N; i++ {
		a := New(cfg, nil, nil)
		if err := a.AddRecords(records); err != nil {
			b.Fatalf("add records: %v", err)
		}
		total += len(records)
	}
	elapsed := time.Since(started)
	b.ReportMetric(float64(total)/elapsed.Seconds(), "records/s")
}

func BenchmarkAddRecordsThroughputParallelWorkers(b *testing.B) {
	records := makeBenchmarkRecords(benchmarkRecordCount)
	cfg := DefaultAnalyzerConfig()
	cfg.Workers = 4

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	total := 0
	for i := 0; i < b.N; i++ {
		a := New(cfg, nil, nil)
		if err := a.AddRecords(records); err != nil {
			b.Fatalf("add records: %v", err)
		}
		total += len(records)
	}
	elapsed := time.Since(started)
	b.ReportMetric(float64(total)/elapsed.Seconds(), "records/s")
}

func makeBenchmarkRecords(n int) []Record {
	queries := []string{
		`SELECT mean(value) FROM cpu WHERE host = 'host-a' AND time >= '2026-06-06T00:00:00Z' GROUP BY time(1m) LIMIT 100`,
		`SELECT * FROM mydb.autogen.cpu`,
		`SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ WHERE region =~ /cn/ LIMIT 1000`,
		`SELECT max(usage_user), min(usage_idle) FROM cpu WHERE time >= now() - 1h GROUP BY host, region SLIMIT 50`,
		`DELETE FROM cpu WHERE time < '2026-01-01T00:00:00Z'`,
	}
	base := time.Date(2026, 6, 6, 8, 0, 0, 0, time.UTC)
	records := make([]Record, n)
	for i := range records {
		records[i] = Record{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Query:     queries[i%len(queries)],
		}
	}
	return records
}
