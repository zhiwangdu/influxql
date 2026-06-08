package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/influxdata/influxql/analyzer"
)

func main() {
	var (
		inputPath         = flag.String("input", "", "JSONL input file path, defaults to stdin")
		inputAPath        = flag.String("input-a", "", "baseline JSONL input file for compare mode")
		inputBPath        = flag.String("input-b", "", "candidate JSONL input file for compare mode")
		configPath        = flag.String("config", "", "JSON config file path")
		windowStart       = flag.String("window-start", "", "RFC3339 lower bound for record timestamp")
		windowEnd         = flag.String("window-end", "", "RFC3339 upper bound for record timestamp")
		outputFmt         = flag.String("output", "json", "output format, only json is supported")
		detailLimit       = flag.Int("detail-limit", 3, "max sample queries per bucket")
		workers           = flag.Int("workers", runtime.GOMAXPROCS(0), "number of concurrent analyzer workers")
		progressEvery     = flag.Int("progress-every", 10000, "print progress every N input records")
		queryCacheSize    = flag.Int("query-cache-size", -1, "max normalized query cache entries, 0 disables cache")
		realtimeThreshold = flag.String("realtime-threshold", "", "max age for realtime query lower bound, for example 2h or 30m")
	)
	flag.Parse()

	if strings.ToLower(*outputFmt) != "json" {
		fatalf("unsupported output format %q", *outputFmt)
	}

	cfg := analyzer.DefaultAnalyzerConfig()
	cfg.DetailLimit = *detailLimit
	cfg.Workers = *workers
	if *queryCacheSize >= 0 {
		cfg.QueryCacheSize = *queryCacheSize
	}
	if *configPath != "" {
		loaded, err := loadConfig(*configPath)
		if err != nil {
			fatalf("load config: %v", err)
		}
		cfg = loaded
		if *detailLimit > 0 {
			cfg.DetailLimit = *detailLimit
		}
		if *queryCacheSize >= 0 {
			cfg.QueryCacheSize = *queryCacheSize
		}
	}
	if strings.TrimSpace(*realtimeThreshold) != "" {
		dur, err := time.ParseDuration(*realtimeThreshold)
		if err != nil {
			fatalf("parse realtime-threshold: %v", err)
		}
		cfg.RealtimeQuery.ThresholdSeconds = int64(dur.Seconds())
	}

	start, err := parseOptionalTime(*windowStart)
	if err != nil {
		fatalf("parse window-start: %v", err)
	}
	end, err := parseOptionalTime(*windowEnd)
	if err != nil {
		fatalf("parse window-end: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if *inputAPath != "" || *inputBPath != "" {
		if *inputAPath == "" || *inputBPath == "" {
			fatalf("compare mode requires both -input-a and -input-b")
		}
		aReport, err := runPath("A", *inputAPath, cfg, start, end, *progressEvery)
		if err != nil {
			fatalf("analyze input-a: %v", err)
		}
		bReport, err := runPath("B", *inputBPath, cfg, start, end, *progressEvery)
		if err != nil {
			fatalf("analyze input-b: %v", err)
		}
		diff := analyzer.CompareReports("A", aReport, "B", bReport)
		if err := encoder.Encode(diff); err != nil {
			fatalf("write output: %v", err)
		}
		return
	}

	if strings.TrimSpace(*inputPath) != "" {
		report, err := runPath("input", *inputPath, cfg, start, end, *progressEvery)
		if err != nil {
			fatalf("analyze input: %v", err)
		}
		if err := encoder.Encode(report); err != nil {
			fatalf("write output: %v", err)
		}
		return
	}

	report, err := run("input", os.Stdin, cfg, start, end, nil, *progressEvery)
	if err != nil {
		fatalf("analyze input: %v", err)
	}
	if err := encoder.Encode(report); err != nil {
		fatalf("write output: %v", err)
	}
}

func runPath(label, path string, cfg analyzer.AnalyzerConfig, windowStart, windowEnd *time.Time, progressEvery int) (analyzer.Report, error) {
	total, err := countRecords(path)
	if err != nil {
		return analyzer.Report{}, err
	}
	reader, closeFn, err := openInput(path)
	if err != nil {
		return analyzer.Report{}, err
	}
	if closeFn != nil {
		defer closeFn()
	}
	return run(label, reader, cfg, windowStart, windowEnd, &total, progressEvery)
}

func run(label string, r io.Reader, cfg analyzer.AnalyzerConfig, windowStart, windowEnd *time.Time, total *int, progressEvery int) (analyzer.Report, error) {
	a := analyzer.New(cfg, windowStart, windowEnd)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	batch := make([]analyzer.Record, 0, 2048)
	progress := newProgressPrinter(label, total, progressEvery)
	progress.start()
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		record, err := decodeRecord(line)
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
		progress.advance(len(batch))
		batch = batch[:0]
	}
	if err := scanner.Err(); err != nil {
		return analyzer.Report{}, err
	}
	if len(batch) > 0 {
		if err := a.AddRecords(batch); err != nil {
			return analyzer.Report{}, err
		}
		progress.advance(len(batch))
	}
	progress.finish()
	return a.Report(), nil
}

func countRecords(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	total := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		total++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return total, nil
}

type inputRecord struct {
	Timestamp json.RawMessage `json:"timestamp"`
	Time      string          `json:"time"`
	Query     *string         `json:"query"`
}

func decodeRecord(line []byte) (analyzer.Record, error) {
	data, err := jsonRecordBytes(line)
	if err != nil {
		return analyzer.Record{}, err
	}

	var input inputRecord
	if err := json.Unmarshal(data, &input); err != nil {
		return analyzer.Record{}, err
	}
	if input.Query == nil {
		return analyzer.Record{}, fmt.Errorf("input record missing query")
	}

	record := analyzer.Record{Query: *input.Query}
	if len(input.Timestamp) > 0 {
		parsed, err := parseRawTime(input.Timestamp)
		if err != nil {
			return analyzer.Record{}, fmt.Errorf("parse timestamp: %w", err)
		}
		record.Timestamp = parsed
	} else if strings.TrimSpace(input.Time) != "" {
		parsed, err := parseTimeString(input.Time)
		if err != nil {
			return analyzer.Record{}, fmt.Errorf("parse timestamp: %w", err)
		}
		record.Timestamp = parsed
	}
	return record, nil
}

func jsonRecordBytes(line []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("input record is empty")
	}
	if trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' {
		return trimmed, nil
	}

	text := string(trimmed)
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("input record is not JSON")
	}
	return []byte(text[start : end+1]), nil
}

func parseRawTime(raw json.RawMessage) (time.Time, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return time.Time{}, fmt.Errorf("timestamp is empty")
	}
	if value[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return time.Time{}, err
		}
		return parseTimeString(text)
	}

	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("unsupported timestamp value %q", value)
	}
	sec, frac := math.Modf(seconds)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC(), nil
}

func parseTimeString(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
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

type progressPrinter struct {
	label         string
	total         *int
	progressEvery int
	processed     int
	nextMark      int
}

func newProgressPrinter(label string, total *int, progressEvery int) *progressPrinter {
	if progressEvery <= 0 {
		progressEvery = 10000
	}
	return &progressPrinter{
		label:         label,
		total:         total,
		progressEvery: progressEvery,
		nextMark:      progressEvery,
	}
}

func (p *progressPrinter) start() {
	if p.total != nil {
		fmt.Fprintf(os.Stderr, "[%s] total records: %d\n", p.label, *p.total)
		return
	}
	fmt.Fprintf(os.Stderr, "[%s] total records: unknown (streaming input)\n", p.label)
}

func (p *progressPrinter) advance(n int) {
	p.processed += n
	if p.processed < p.nextMark {
		return
	}
	p.print("progress")
	for p.nextMark <= p.processed {
		p.nextMark += p.progressEvery
	}
}

func (p *progressPrinter) finish() {
	p.print("done")
}

func (p *progressPrinter) print(stage string) {
	if p.total != nil {
		fmt.Fprintf(os.Stderr, "[%s] %s: analyzed %d/%d records\n", p.label, stage, p.processed, *p.total)
		return
	}
	fmt.Fprintf(os.Stderr, "[%s] %s: analyzed %d records\n", p.label, stage, p.processed)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
