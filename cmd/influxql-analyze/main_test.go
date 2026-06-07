package main

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeRecordSupportsExecutorLogLine(t *testing.T) {
	line := []byte(`{"level":"info","time":"2026-06-06T16:10:12.675546+08:00","msg":"Executing query","hostname":"127.0.0.1:8086","service":"executor","query":"SELECT * FROM mydb.autogen.cpu","batch":1,"location":"query/executor.go:535","repeated":1}`)

	record, err := decodeRecord(line)
	if err != nil {
		t.Fatalf("decodeRecord returned error: %v", err)
	}

	if got, want := record.Query, "SELECT * FROM mydb.autogen.cpu"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	wantTime, err := time.Parse(time.RFC3339Nano, "2026-06-06T16:10:12.675546+08:00")
	if err != nil {
		t.Fatal(err)
	}
	if !record.Timestamp.Equal(wantTime) {
		t.Fatalf("timestamp = %s, want %s", record.Timestamp, wantTime)
	}
}

func TestDecodeRecordExtractsEmbeddedJSONLog(t *testing.T) {
	line := []byte(`2026-06-06 executor {"time":"2026-06-06T16:10:12.675546+08:00","query":"SHOW MEASUREMENTS"} trailing text`)

	record, err := decodeRecord(line)
	if err != nil {
		t.Fatalf("decodeRecord returned error: %v", err)
	}
	if got, want := record.Query, "SHOW MEASUREMENTS"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	if record.Timestamp.IsZero() {
		t.Fatal("timestamp was not parsed from time field")
	}
}

func TestDecodeRecordReportsMissingJSON(t *testing.T) {
	_, err := decodeRecord([]byte(`not a json log line`))
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("error = %v, want not JSON error", err)
	}
}
