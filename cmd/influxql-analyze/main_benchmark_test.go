package main

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkDecodeExecutorLogRecordThroughput(b *testing.B) {
	lines := makeBenchmarkExecutorLogLines(8192)

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	total := 0
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			if _, err := decodeRecord(line); err != nil {
				b.Fatalf("decode record: %v", err)
			}
		}
		total += len(lines)
	}
	elapsed := time.Since(started)
	b.ReportMetric(float64(total)/elapsed.Seconds(), "records/s")
}

func makeBenchmarkExecutorLogLines(n int) [][]byte {
	lines := make([][]byte, n)
	queries := []string{
		`SELECT * FROM mydb.autogen.cpu`,
		`SELECT mean(value) FROM cpu WHERE host = 'host-a' AND time >= '2026-06-06T00:00:00Z' GROUP BY time(1m)`,
		`SHOW TAG VALUES FROM cpu WITH KEY =~ /host.*/ LIMIT 1000`,
	}
	for i := range lines {
		lines[i] = []byte(fmt.Sprintf(`{"level":"info","time":"2026-06-06T16:10:12.675546+08:00","msg":"Executing query","hostname":"127.0.0.1:8086","service":"executor","query":%q,"batch":%d,"location":"query/executor.go:535","repeated":1}`, queries[i%len(queries)], i))
	}
	return lines
}
