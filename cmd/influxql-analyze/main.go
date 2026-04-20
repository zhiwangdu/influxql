package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/influxdata/influxql/analyzer"
)

func main() {
	var (
		inputPath   = flag.String("input", "", "JSONL input file path, defaults to stdin")
		configPath  = flag.String("config", "", "JSON config file path")
		windowStart = flag.String("window-start", "", "RFC3339 lower bound for record timestamp")
		windowEnd   = flag.String("window-end", "", "RFC3339 upper bound for record timestamp")
		outputFmt   = flag.String("output", "json", "output format, only json is supported")
		detailLimit = flag.Int("detail-limit", 3, "max sample queries per bucket")
		workers     = flag.Int("workers", runtime.GOMAXPROCS(0), "number of concurrent analyzer workers")
	)
	flag.Parse()

	if strings.ToLower(*outputFmt) != "json" {
		fatalf("unsupported output format %q", *outputFmt)
	}

	cfg := analyzer.DefaultAnalyzerConfig()
	cfg.DetailLimit = *detailLimit
	cfg.Workers = *workers
	if *configPath != "" {
		loaded, err := loadConfig(*configPath)
		if err != nil {
			fatalf("load config: %v", err)
		}
		cfg = loaded
		if *detailLimit > 0 {
			cfg.DetailLimit = *detailLimit
		}
	}

	start, err := parseOptionalTime(*windowStart)
	if err != nil {
		fatalf("parse window-start: %v", err)
	}
	end, err := parseOptionalTime(*windowEnd)
	if err != nil {
		fatalf("parse window-end: %v", err)
	}

	reader, closeFn, err := openInput(*inputPath)
	if err != nil {
		fatalf("open input: %v", err)
	}
	if closeFn != nil {
		defer closeFn()
	}

	report, err := run(reader, cfg, start, end)
	if err != nil {
		fatalf("analyze input: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fatalf("write output: %v", err)
	}
}

func run(r io.Reader, cfg analyzer.AnalyzerConfig, windowStart, windowEnd *time.Time) (analyzer.Report, error) {
	a := analyzer.New(cfg, windowStart, windowEnd)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	batch := make([]analyzer.Record, 0, 2048)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		record, err := decodeRecord([]byte(line))
		if err != nil {
			return analyzer.Report{}, err
		}
		batch = append(batch, record)
		if len(batch) < cap(batch) {
			continue
		}
		if err := a.AddRecords(batch); err != nil {
			return analyzer.Report{}, err
		}
		batch = batch[:0]
	}
	if err := scanner.Err(); err != nil {
		return analyzer.Report{}, err
	}
	if len(batch) > 0 {
		if err := a.AddRecords(batch); err != nil {
			return analyzer.Report{}, err
		}
	}
	return a.Report(), nil
}

func decodeRecord(line []byte) (analyzer.Record, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return analyzer.Record{}, err
	}

	record := analyzer.Record{Raw: raw}
	query, ok := raw["query"]
	if !ok {
		return analyzer.Record{}, fmt.Errorf("input record missing query")
	}

	queryString, ok := query.(string)
	if !ok {
		return analyzer.Record{}, fmt.Errorf("input query must be a string")
	}
	record.Query = queryString

	if ts, ok := raw["timestamp"]; ok {
		parsed, err := parseFlexibleTime(ts)
		if err != nil {
			return analyzer.Record{}, fmt.Errorf("parse timestamp: %w", err)
		}
		record.Timestamp = parsed
	}
	return record, nil
}

func parseFlexibleTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case string:
		return time.Parse(time.RFC3339, v)
	case float64:
		sec := int64(v)
		return time.Unix(sec, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", value)
	}
}

func parseOptionalTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func loadConfig(path string) (analyzer.AnalyzerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return analyzer.AnalyzerConfig{}, err
	}
	cfg := analyzer.DefaultAnalyzerConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return analyzer.AnalyzerConfig{}, err
	}
	return cfg, nil
}

func openInput(path string) (io.Reader, func() error, error) {
	if strings.TrimSpace(path) == "" {
		return os.Stdin, nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
