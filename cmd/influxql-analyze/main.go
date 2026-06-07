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
		inputPath     = flag.String("input", "", "JSONL input file path, defaults to stdin")
		inputAPath    = flag.String("input-a", "", "baseline JSONL input file for compare mode")
		inputBPath    = flag.String("input-b", "", "candidate JSONL input file for compare mode")
		configPath    = flag.String("config", "", "JSON config file path")
		windowStart   = flag.String("window-start", "", "RFC3339 lower bound for record timestamp")
		windowEnd     = flag.String("window-end", "", "RFC3339 upper bound for record timestamp")
		outputFmt     = flag.String("output", "json", "output format, only json is supported")
		detailLimit   = flag.Int("detail-limit", 3, "max sample queries per bucket")
		workers       = flag.Int("workers", runtime.GOMAXPROCS(0), "number of concurrent analyzer workers")
		progressEvery = flag.Int("progress-every", 10000, "print progress every N input records")
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
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		total++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return total, nil
}

func decodeRecord(line []byte) (analyzer.Record, error) {
	raw, err := decodeRawRecord(line)
	if err != nil {
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

	if ts, ok := recordTimeValue(raw); ok {
		parsed, err := parseFlexibleTime(ts)
		if err != nil {
			return analyzer.Record{}, fmt.Errorf("parse timestamp: %w", err)
		}
		record.Timestamp = parsed
	}
	return record, nil
}

func decodeRawRecord(line []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err == nil {
		return raw, nil
	}

	text := strings.TrimSpace(string(line))
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("input record is not JSON")
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func recordTimeValue(raw map[string]any) (any, bool) {
	if ts, ok := raw["timestamp"]; ok {
		return ts, true
	}
	if ts, ok := raw["time"]; ok {
		return ts, true
	}
	return nil, false
}

func parseFlexibleTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case string:
		return time.Parse(time.RFC3339Nano, v)
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
