package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	influxql "github.com/influxdata/influxql"
)

const (
	RuleNoTimeFilter            = "no_time_filter"
	RuleHasRegex                = "has_regex"
	RuleHasWildcard             = "has_wildcard"
	RuleLargeLimit              = "large_limit"
	RuleGroupByHighCardinality  = "group_by_high_cardinality_risk"
	RuleMetaQuery               = "meta_query"
	RuleWriteOrDestructive      = "write_or_destructive"
	defaultLargeLimitThreshold  = 1000
	defaultGroupByTagThreshold  = 2
	defaultDetailLimit          = 3
	normalizedStringPlaceholder = "str"
	normalizedIdentPlaceholder  = "tag"
)

var normalizedRegex = regexp.MustCompile(".*")

type Record struct {
	Timestamp time.Time      `json:"timestamp"`
	Query     string         `json:"query"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type FeatureSet struct {
	StatementType        string `json:"statement_type"`
	HasTimeFilter        bool   `json:"has_time_filter"`
	TimeRangeExtracted   bool   `json:"time_range_extracted"`
	HasRegex             bool   `json:"has_regex"`
	HasWildcard          bool   `json:"has_wildcard"`
	Limit                int    `json:"limit,omitempty"`
	SLimit               int    `json:"slimit,omitempty"`
	GroupByCount         int    `json:"group_by_count,omitempty"`
	NonTimeGroupByCount  int    `json:"non_time_group_by_count,omitempty"`
	IsSelectInto         bool   `json:"is_select_into,omitempty"`
	IsMetaQuery          bool   `json:"is_meta_query,omitempty"`
	IsWriteOrDestructive bool   `json:"is_write_or_destructive,omitempty"`
}

type NormalizeResult struct {
	StatementType string     `json:"statement_type"`
	Normalized    string     `json:"normalized"`
	Fingerprint   string     `json:"fingerprint"`
	Features      FeatureSet `json:"features"`
}

type RuleMatch struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type RuleConfig struct {
	Enabled             map[string]bool `json:"enabled,omitempty"`
	LargeLimitThreshold int             `json:"large_limit_threshold,omitempty"`
	GroupByTagThreshold int             `json:"group_by_tag_threshold,omitempty"`
}

type AnalyzerConfig struct {
	DetailLimit int        `json:"detail_limit,omitempty"`
	Workers     int        `json:"workers,omitempty"`
	Rules       RuleConfig `json:"rules,omitempty"`
}

type FingerprintSummary struct {
	Fingerprint     string   `json:"fingerprint"`
	StatementType   string   `json:"statement_type"`
	NormalizedQuery string   `json:"normalized_query"`
	Count           int      `json:"count"`
	SampleQueries   []string `json:"sample_queries,omitempty"`
	Rules           []string `json:"rules,omitempty"`
}

type RuleSummary struct {
	Rule         string   `json:"rule"`
	Count        int      `json:"count"`
	Fingerprints []string `json:"fingerprints,omitempty"`
}

type ParseErrorSummary struct {
	Error         string   `json:"error"`
	Count         int      `json:"count"`
	SampleQueries []string `json:"sample_queries,omitempty"`
}

type Report struct {
	WindowStart     *time.Time           `json:"window_start,omitempty"`
	WindowEnd       *time.Time           `json:"window_end,omitempty"`
	TotalRecords    int                  `json:"total_records"`
	RecordsInWindow int                  `json:"records_in_window"`
	TotalStatements int                  `json:"total_statements"`
	ParseErrorCount int                  `json:"parse_error_count"`
	Fingerprints    []FingerprintSummary `json:"fingerprints"`
	SpecialRules    []RuleSummary        `json:"special_rules"`
	ParseErrors     []ParseErrorSummary  `json:"parse_errors,omitempty"`
}

type Analyzer struct {
	cfg         AnalyzerConfig
	windowStart *time.Time
	windowEnd   *time.Time
	report      Report

	fingerprints map[string]*FingerprintSummary
	rules        map[string]*RuleSummary
	parseErrors  map[string]*ParseErrorSummary
}

func DefaultAnalyzerConfig() AnalyzerConfig {
	return AnalyzerConfig{
		DetailLimit: defaultDetailLimit,
		Rules: RuleConfig{
			LargeLimitThreshold: defaultLargeLimitThreshold,
			GroupByTagThreshold: defaultGroupByTagThreshold,
		},
	}
}

func New(cfg AnalyzerConfig, windowStart, windowEnd *time.Time) *Analyzer {
	cfg = applyDefaults(cfg)
	return &Analyzer{
		cfg:         cfg,
		windowStart: windowStart,
		windowEnd:   windowEnd,
		report: Report{
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		},
		fingerprints: make(map[string]*FingerprintSummary),
		rules:        make(map[string]*RuleSummary),
		parseErrors:  make(map[string]*ParseErrorSummary),
	}
}

func Analyze(records []Record, cfg AnalyzerConfig, windowStart, windowEnd *time.Time) (*Report, error) {
	a := New(cfg, windowStart, windowEnd)
	if err := a.AddRecords(records); err != nil {
		return nil, err
	}
	report := a.Report()
	return &report, nil
}

func NormalizeQuery(query string) ([]NormalizeResult, error) {
	parsed, err := influxql.ParseQuery(query)
	if err != nil {
		return nil, err
	}

	results := make([]NormalizeResult, 0, len(parsed.Statements))
	for _, stmt := range parsed.Statements {
		results = append(results, normalizeStatement(stmt))
	}
	return results, nil
}

func (a *Analyzer) AddRecord(record Record) error {
	a.report.TotalRecords++
	if !a.withinWindow(record.Timestamp) {
		return nil
	}
	a.report.RecordsInWindow++

	query := strings.TrimSpace(record.Query)
	if query == "" {
		return nil
	}

	results, err := NormalizeQuery(query)
	if err != nil {
		a.report.ParseErrorCount++
		a.addParseError(err.Error(), query)
		return nil
	}

	for _, result := range results {
		a.report.TotalStatements++
		a.addFingerprint(result, query)
		for _, match := range matchRules(result.Features, a.cfg.Rules) {
			a.addRuleMatch(match.Name, result.Fingerprint)
			a.addRuleToFingerprint(result.Fingerprint, match.Name)
		}
	}
	return nil
}

func (a *Analyzer) AddRecords(records []Record) error {
	if len(records) == 0 {
		return nil
	}

	workers := a.cfg.Workers
	if workers <= 1 || len(records) == 1 {
		for _, record := range records {
			if err := a.AddRecord(record); err != nil {
				return err
			}
		}
		return nil
	}

	if workers > len(records) {
		workers = len(records)
	}

	chunkSize := (len(records) + workers - 1) / workers
	partials := make([]*Analyzer, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunkSize
		if start >= len(records) {
			break
		}

		end := start + chunkSize
		if end > len(records) {
			end = len(records)
		}

		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			partial := New(a.cfg, a.windowStart, a.windowEnd)
			for _, record := range records[start:end] {
				if err := partial.AddRecord(record); err != nil {
					errs[idx] = fmt.Errorf("worker %d: %w", idx, err)
					return
				}
			}
			partials[idx] = partial
		}(i, start, end)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	for _, partial := range partials {
		if partial == nil {
			continue
		}
		a.merge(partial)
	}
	return nil
}

func (a *Analyzer) Report() Report {
	report := a.report
	report.Fingerprints = sortedFingerprints(a.fingerprints)
	report.SpecialRules = sortedRules(a.rules)
	report.ParseErrors = sortedParseErrors(a.parseErrors)
	return report
}

func (a *Analyzer) merge(other *Analyzer) {
	a.report.TotalRecords += other.report.TotalRecords
	a.report.RecordsInWindow += other.report.RecordsInWindow
	a.report.TotalStatements += other.report.TotalStatements
	a.report.ParseErrorCount += other.report.ParseErrorCount

	for fp, otherBucket := range other.fingerprints {
		bucket := a.fingerprints[fp]
		if bucket == nil {
			copyBucket := *otherBucket
			copyBucket.SampleQueries = append([]string(nil), otherBucket.SampleQueries...)
			copyBucket.Rules = append([]string(nil), otherBucket.Rules...)
			a.fingerprints[fp] = &copyBucket
			continue
		}
		bucket.Count += otherBucket.Count
		for _, sample := range otherBucket.SampleQueries {
			appendSample(&bucket.SampleQueries, sample, a.cfg.DetailLimit)
		}
		for _, rule := range otherBucket.Rules {
			appendUnique(&bucket.Rules, rule)
		}
	}

	for rule, otherBucket := range other.rules {
		bucket := a.rules[rule]
		if bucket == nil {
			copyBucket := *otherBucket
			copyBucket.Fingerprints = append([]string(nil), otherBucket.Fingerprints...)
			a.rules[rule] = &copyBucket
			continue
		}
		bucket.Count += otherBucket.Count
		for _, fp := range otherBucket.Fingerprints {
			appendUnique(&bucket.Fingerprints, fp)
		}
	}

	for message, otherBucket := range other.parseErrors {
		bucket := a.parseErrors[message]
		if bucket == nil {
			copyBucket := *otherBucket
			copyBucket.SampleQueries = append([]string(nil), otherBucket.SampleQueries...)
			a.parseErrors[message] = &copyBucket
			continue
		}
		bucket.Count += otherBucket.Count
		for _, sample := range otherBucket.SampleQueries {
			appendSample(&bucket.SampleQueries, sample, a.cfg.DetailLimit)
		}
	}
}

func normalizeStatement(stmt influxql.Statement) NormalizeResult {
	features := extractFeatures(stmt)
	normalizeStatementInPlace(stmt)
	normalized := stmt.String()
	return NormalizeResult{
		StatementType: features.StatementType,
		Normalized:    normalized,
		Fingerprint:   fingerprint(normalized),
		Features:      features,
	}
}

func fingerprint(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func applyDefaults(cfg AnalyzerConfig) AnalyzerConfig {
	defaults := DefaultAnalyzerConfig()
	if cfg.DetailLimit <= 0 {
		cfg.DetailLimit = defaults.DetailLimit
	}
	if cfg.Rules.LargeLimitThreshold <= 0 {
		cfg.Rules.LargeLimitThreshold = defaults.Rules.LargeLimitThreshold
	}
	if cfg.Rules.GroupByTagThreshold <= 0 {
		cfg.Rules.GroupByTagThreshold = defaults.Rules.GroupByTagThreshold
	}
	return cfg
}

func (a *Analyzer) withinWindow(ts time.Time) bool {
	if ts.IsZero() {
		return true
	}
	if a.windowStart != nil && ts.Before(*a.windowStart) {
		return false
	}
	if a.windowEnd != nil && ts.After(*a.windowEnd) {
		return false
	}
	return true
}

func (a *Analyzer) addFingerprint(result NormalizeResult, rawQuery string) {
	bucket := a.fingerprints[result.Fingerprint]
	if bucket == nil {
		bucket = &FingerprintSummary{
			Fingerprint:     result.Fingerprint,
			StatementType:   result.StatementType,
			NormalizedQuery: result.Normalized,
		}
		a.fingerprints[result.Fingerprint] = bucket
	}
	bucket.Count++
	appendSample(&bucket.SampleQueries, rawQuery, a.cfg.DetailLimit)
}

func (a *Analyzer) addRuleToFingerprint(fingerprint, rule string) {
	bucket := a.fingerprints[fingerprint]
	if bucket == nil {
		return
	}
	appendUnique(&bucket.Rules, rule)
}

func (a *Analyzer) addRuleMatch(rule, fingerprint string) {
	bucket := a.rules[rule]
	if bucket == nil {
		bucket = &RuleSummary{Rule: rule}
		a.rules[rule] = bucket
	}
	bucket.Count++
	appendUnique(&bucket.Fingerprints, fingerprint)
}

func (a *Analyzer) addParseError(message, rawQuery string) {
	bucket := a.parseErrors[message]
	if bucket == nil {
		bucket = &ParseErrorSummary{Error: message}
		a.parseErrors[message] = bucket
	}
	bucket.Count++
	appendSample(&bucket.SampleQueries, rawQuery, a.cfg.DetailLimit)
}

func appendSample(samples *[]string, value string, limit int) {
	if value == "" || len(*samples) >= limit {
		return
	}
	*samples = append(*samples, value)
}

func appendUnique(values *[]string, value string) {
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func sortedFingerprints(src map[string]*FingerprintSummary) []FingerprintSummary {
	out := make([]FingerprintSummary, 0, len(src))
	for _, bucket := range src {
		copyBucket := *bucket
		sort.Strings(copyBucket.Rules)
		out = append(out, copyBucket)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

func sortedRules(src map[string]*RuleSummary) []RuleSummary {
	out := make([]RuleSummary, 0, len(src))
	for _, bucket := range src {
		copyBucket := *bucket
		sort.Strings(copyBucket.Fingerprints)
		out = append(out, copyBucket)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func sortedParseErrors(src map[string]*ParseErrorSummary) []ParseErrorSummary {
	out := make([]ParseErrorSummary, 0, len(src))
	for _, bucket := range src {
		out = append(out, *bucket)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Error < out[j].Error
	})
	return out
}
