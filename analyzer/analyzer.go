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
	RuleNotRealtimeQuery        = "not_realtime_query"
	defaultLargeLimitThreshold  = 1000
	defaultGroupByTagThreshold  = 2
	defaultDetailLimit          = 3
	defaultQueryCacheSize       = 10000
	defaultRealtimeThreshold    = 24 * time.Hour
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

type RealtimeQueryConfig struct {
	ThresholdSeconds int64 `json:"threshold_seconds,omitempty"`
}

type AnalyzerConfig struct {
	DetailLimit    int                 `json:"detail_limit,omitempty"`
	Workers        int                 `json:"workers,omitempty"`
	QueryCacheSize int                 `json:"query_cache_size,omitempty"`
	Rules          RuleConfig          `json:"rules,omitempty"`
	RealtimeQuery  RealtimeQueryConfig `json:"realtime_query,omitempty"`
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

type RealtimeTimeRangeSummary struct {
	Min           *time.Time `json:"min,omitempty"`
	Max           *time.Time `json:"max,omitempty"`
	WindowSeconds *float64   `json:"window_seconds,omitempty"`
}

type RealtimeQuerySample struct {
	Query     string                    `json:"query"`
	Reason    string                    `json:"reason"`
	LogTime   *time.Time                `json:"log_time,omitempty"`
	TimeRange *RealtimeTimeRangeSummary `json:"time_range,omitempty"`
}

type RealtimeQuerySummary struct {
	ThresholdSeconds  int64                 `json:"threshold_seconds"`
	Total             int                   `json:"total"`
	Realtime          int                   `json:"realtime"`
	NonRealtime       int                   `json:"non_realtime"`
	Unknown           int                   `json:"unknown"`
	AllRealtime       bool                  `json:"all_realtime"`
	SampleNonRealtime []RealtimeQuerySample `json:"sample_non_realtime,omitempty"`
	SampleUnknown     []RealtimeQuerySample `json:"sample_unknown,omitempty"`
}

type Report struct {
	WindowStart     *time.Time           `json:"window_start,omitempty"`
	WindowEnd       *time.Time           `json:"window_end,omitempty"`
	ObservedStart   *time.Time           `json:"observed_start,omitempty"`
	ObservedEnd     *time.Time           `json:"observed_end,omitempty"`
	TotalRecords    int                  `json:"total_records"`
	RecordsInWindow int                  `json:"records_in_window"`
	TotalStatements int                  `json:"total_statements"`
	ParseErrorCount int                  `json:"parse_error_count"`
	Fingerprints    []FingerprintSummary `json:"fingerprints"`
	SpecialRules    []RuleSummary        `json:"special_rules"`
	ParseErrors     []ParseErrorSummary  `json:"parse_errors,omitempty"`
	RealtimeQuery   RealtimeQuerySummary `json:"realtime_query"`
}

type Analyzer struct {
	cfg         AnalyzerConfig
	windowStart *time.Time
	windowEnd   *time.Time
	report      Report

	fingerprints map[string]*FingerprintSummary
	rules        map[string]*RuleSummary
	parseErrors  map[string]*ParseErrorSummary

	queryCache     map[string]queryAnalysis
	queryCacheKeys []string
	queryCacheNext int
}

type BatchStats struct {
	Label             string     `json:"label"`
	TotalRecords      int        `json:"total_records"`
	RecordsInWindow   int        `json:"records_in_window"`
	TotalStatements   int        `json:"total_statements"`
	ParseErrorCount   int        `json:"parse_error_count"`
	ObservedStart     *time.Time `json:"observed_start,omitempty"`
	ObservedEnd       *time.Time `json:"observed_end,omitempty"`
	WindowStart       *time.Time `json:"window_start,omitempty"`
	WindowEnd         *time.Time `json:"window_end,omitempty"`
	EffectiveDuration float64    `json:"effective_duration_seconds"`
	QPS               float64    `json:"qps"`
}

type FingerprintDelta struct {
	Fingerprint     string   `json:"fingerprint"`
	StatementType   string   `json:"statement_type"`
	NormalizedQuery string   `json:"normalized_query"`
	Status          string   `json:"status"`
	CountA          int      `json:"count_a"`
	CountB          int      `json:"count_b"`
	CountDelta      int      `json:"count_delta"`
	QPSA            float64  `json:"qps_a"`
	QPSB            float64  `json:"qps_b"`
	QPSDelta        float64  `json:"qps_delta"`
	Rules           []string `json:"rules,omitempty"`
}

type RuleDelta struct {
	Rule       string  `json:"rule"`
	CountA     int     `json:"count_a"`
	CountB     int     `json:"count_b"`
	CountDelta int     `json:"count_delta"`
	QPSA       float64 `json:"qps_a"`
	QPSB       float64 `json:"qps_b"`
	QPSDelta   float64 `json:"qps_delta"`
}

type CompareReport struct {
	BatchA              BatchStats         `json:"batch_a"`
	BatchB              BatchStats         `json:"batch_b"`
	StatementDelta      int                `json:"statement_delta"`
	QPSDelta            float64            `json:"qps_delta"`
	NewFingerprints     []FingerprintDelta `json:"new_fingerprints"`
	RemovedFingerprints []FingerprintDelta `json:"removed_fingerprints"`
	ChangedFingerprints []FingerprintDelta `json:"changed_fingerprints"`
	RuleDeltas          []RuleDelta        `json:"rule_deltas,omitempty"`
}

func DefaultAnalyzerConfig() AnalyzerConfig {
	return AnalyzerConfig{
		DetailLimit:    defaultDetailLimit,
		QueryCacheSize: defaultQueryCacheSize,
		Rules: RuleConfig{
			LargeLimitThreshold: defaultLargeLimitThreshold,
			GroupByTagThreshold: defaultGroupByTagThreshold,
		},
		RealtimeQuery: RealtimeQueryConfig{
			ThresholdSeconds: int64(defaultRealtimeThreshold.Seconds()),
		},
	}
}

func New(cfg AnalyzerConfig, windowStart, windowEnd *time.Time) *Analyzer {
	cfg = applyDefaults(cfg)
	a := &Analyzer{
		cfg:         cfg,
		windowStart: windowStart,
		windowEnd:   windowEnd,
		report: Report{
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
			RealtimeQuery: RealtimeQuerySummary{
				ThresholdSeconds: cfg.RealtimeQuery.ThresholdSeconds,
			},
		},
		fingerprints: make(map[string]*FingerprintSummary),
		rules:        make(map[string]*RuleSummary),
		parseErrors:  make(map[string]*ParseErrorSummary),
	}
	if cfg.QueryCacheSize > 0 {
		a.queryCache = make(map[string]queryAnalysis, cfg.QueryCacheSize)
		a.queryCacheKeys = make([]string, 0, cfg.QueryCacheSize)
	}
	return a
}

func Analyze(records []Record, cfg AnalyzerConfig, windowStart, windowEnd *time.Time) (*Report, error) {
	a := New(cfg, windowStart, windowEnd)
	if err := a.AddRecords(records); err != nil {
		return nil, err
	}
	report := a.Report()
	return &report, nil
}

func CompareReports(aLabel string, a Report, bLabel string, b Report) CompareReport {
	statsA := makeBatchStats(aLabel, a)
	statsB := makeBatchStats(bLabel, b)

	aByFP := make(map[string]FingerprintSummary, len(a.Fingerprints))
	for _, fp := range a.Fingerprints {
		aByFP[fp.Fingerprint] = fp
	}
	bByFP := make(map[string]FingerprintSummary, len(b.Fingerprints))
	for _, fp := range b.Fingerprints {
		bByFP[fp.Fingerprint] = fp
	}

	var added []FingerprintDelta
	var removed []FingerprintDelta
	var changed []FingerprintDelta

	for fp, bItem := range bByFP {
		if aItem, ok := aByFP[fp]; ok {
			if aItem.Count != bItem.Count {
				changed = append(changed, makeFingerprintDelta(aItem, bItem, statsA.EffectiveDuration, statsB.EffectiveDuration, "changed"))
			}
			continue
		}
		zero := FingerprintSummary{}
		added = append(added, makeFingerprintDelta(zero, bItem, statsA.EffectiveDuration, statsB.EffectiveDuration, "added"))
	}
	for fp, aItem := range aByFP {
		if _, ok := bByFP[fp]; ok {
			continue
		}
		zero := FingerprintSummary{}
		removed = append(removed, makeFingerprintDelta(aItem, zero, statsA.EffectiveDuration, statsB.EffectiveDuration, "removed"))
	}

	aRules := make(map[string]RuleSummary, len(a.SpecialRules))
	for _, item := range a.SpecialRules {
		aRules[item.Rule] = item
	}
	bRules := make(map[string]RuleSummary, len(b.SpecialRules))
	for _, item := range b.SpecialRules {
		bRules[item.Rule] = item
	}

	ruleSet := make(map[string]struct{}, len(aRules)+len(bRules))
	for rule := range aRules {
		ruleSet[rule] = struct{}{}
	}
	for rule := range bRules {
		ruleSet[rule] = struct{}{}
	}

	ruleDeltas := make([]RuleDelta, 0, len(ruleSet))
	for rule := range ruleSet {
		aCount := aRules[rule].Count
		bCount := bRules[rule].Count
		if aCount == bCount {
			continue
		}
		qpsA := qpsForCount(aCount, statsA.EffectiveDuration)
		qpsB := qpsForCount(bCount, statsB.EffectiveDuration)
		ruleDeltas = append(ruleDeltas, RuleDelta{
			Rule:       rule,
			CountA:     aCount,
			CountB:     bCount,
			CountDelta: bCount - aCount,
			QPSA:       qpsA,
			QPSB:       qpsB,
			QPSDelta:   qpsB - qpsA,
		})
	}

	sortFingerprintDeltas(added)
	sortFingerprintDeltas(removed)
	sortFingerprintDeltas(changed)
	sortRuleDeltas(ruleDeltas)

	return CompareReport{
		BatchA:              statsA,
		BatchB:              statsB,
		StatementDelta:      b.TotalStatements - a.TotalStatements,
		QPSDelta:            statsB.QPS - statsA.QPS,
		NewFingerprints:     added,
		RemovedFingerprints: removed,
		ChangedFingerprints: changed,
		RuleDeltas:          ruleDeltas,
	}
}

func NormalizeQuery(query string) ([]NormalizeResult, error) {
	analysis, err := analyzeQuery(query)
	if err != nil {
		return nil, err
	}
	return analysis.Results, nil
}

func analyzeQuery(query string) (queryAnalysis, error) {
	parsed, err := influxql.ParseQuery(query)
	if err != nil {
		return queryAnalysis{}, err
	}

	analysis := queryAnalysis{
		Results:        make([]NormalizeResult, 0, len(parsed.Statements)),
		RealtimeChecks: make([]realtimeCheck, 0, len(parsed.Statements)),
	}
	for _, stmt := range parsed.Statements {
		analysis.RealtimeChecks = append(analysis.RealtimeChecks, extractRealtimeCheck(stmt))
		analysis.Results = append(analysis.Results, normalizeStatement(stmt))
	}
	return analysis, nil
}

func (a *Analyzer) analyzeQuery(query string) (queryAnalysis, error) {
	if a.queryCache == nil {
		return analyzeQuery(query)
	}
	if analysis, ok := a.queryCache[query]; ok {
		return analysis, nil
	}

	analysis, err := analyzeQuery(query)
	if err != nil {
		return queryAnalysis{}, err
	}
	a.addQueryCacheEntry(query, analysis)
	return analysis, nil
}

func (a *Analyzer) addQueryCacheEntry(query string, analysis queryAnalysis) {
	if a.cfg.QueryCacheSize <= 0 {
		return
	}
	if _, ok := a.queryCache[query]; ok {
		return
	}
	if len(a.queryCacheKeys) < a.cfg.QueryCacheSize {
		a.queryCacheKeys = append(a.queryCacheKeys, query)
		a.queryCache[query] = analysis
		return
	}

	evict := a.queryCacheKeys[a.queryCacheNext]
	delete(a.queryCache, evict)
	a.queryCacheKeys[a.queryCacheNext] = query
	a.queryCache[query] = analysis
	a.queryCacheNext++
	if a.queryCacheNext >= len(a.queryCacheKeys) {
		a.queryCacheNext = 0
	}
}

func (a *Analyzer) AddRecord(record Record) error {
	a.report.TotalRecords++
	if !a.withinWindow(record.Timestamp) {
		return nil
	}
	a.report.RecordsInWindow++
	a.trackObservedTime(record.Timestamp)

	query := strings.TrimSpace(record.Query)
	if query == "" {
		return nil
	}

	analysis, err := a.analyzeQuery(query)
	if err != nil {
		a.report.ParseErrorCount++
		a.addParseError(err.Error(), query)
		return nil
	}

	for idx, result := range analysis.Results {
		a.report.TotalStatements++
		a.addFingerprint(result, query)
		realtime := a.evaluateRealtime(realtimeCheckAt(analysis.RealtimeChecks, idx), record.Timestamp, query)
		for _, match := range matchRules(result.Features, a.cfg.Rules) {
			a.addRuleMatch(match.Name, result.Fingerprint)
			a.addRuleToFingerprint(result.Fingerprint, match.Name)
		}
		if realtime.Status == realtimeStatusNonRealtime && ruleEnabled(a.cfg.Rules, RuleNotRealtimeQuery) {
			a.addRuleMatch(RuleNotRealtimeQuery, result.Fingerprint)
			a.addRuleToFingerprint(result.Fingerprint, RuleNotRealtimeQuery)
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
	report.RealtimeQuery.AllRealtime = report.RealtimeQuery.Total > 0 && report.RealtimeQuery.NonRealtime == 0 && report.RealtimeQuery.Unknown == 0
	return report
}

func (a *Analyzer) merge(other *Analyzer) {
	a.mergeObservedTime(other.report.ObservedStart, other.report.ObservedEnd)
	a.report.TotalRecords += other.report.TotalRecords
	a.report.RecordsInWindow += other.report.RecordsInWindow
	a.report.TotalStatements += other.report.TotalStatements
	a.report.ParseErrorCount += other.report.ParseErrorCount
	a.mergeRealtimeQuery(other.report.RealtimeQuery)

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

func (a *Analyzer) trackObservedTime(ts time.Time) {
	if ts.IsZero() {
		return
	}
	if a.report.ObservedStart == nil || ts.Before(*a.report.ObservedStart) {
		start := ts
		a.report.ObservedStart = &start
	}
	if a.report.ObservedEnd == nil || ts.After(*a.report.ObservedEnd) {
		end := ts
		a.report.ObservedEnd = &end
	}
}

func (a *Analyzer) mergeObservedTime(start, end *time.Time) {
	if start != nil {
		a.trackObservedTime(*start)
	}
	if end != nil {
		a.trackObservedTime(*end)
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
	if cfg.QueryCacheSize < 0 {
		cfg.QueryCacheSize = 0
	}
	if cfg.RealtimeQuery.ThresholdSeconds <= 0 {
		cfg.RealtimeQuery.ThresholdSeconds = defaults.RealtimeQuery.ThresholdSeconds
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

func makeBatchStats(label string, report Report) BatchStats {
	duration := effectiveDurationSeconds(report)
	return BatchStats{
		Label:             label,
		TotalRecords:      report.TotalRecords,
		RecordsInWindow:   report.RecordsInWindow,
		TotalStatements:   report.TotalStatements,
		ParseErrorCount:   report.ParseErrorCount,
		ObservedStart:     report.ObservedStart,
		ObservedEnd:       report.ObservedEnd,
		WindowStart:       report.WindowStart,
		WindowEnd:         report.WindowEnd,
		EffectiveDuration: duration,
		QPS:               qpsForCount(report.TotalStatements, duration),
	}
}

func effectiveDurationSeconds(report Report) float64 {
	if report.WindowStart != nil && report.WindowEnd != nil && report.WindowEnd.After(*report.WindowStart) {
		return report.WindowEnd.Sub(*report.WindowStart).Seconds()
	}
	if report.ObservedStart != nil && report.ObservedEnd != nil {
		dur := report.ObservedEnd.Sub(*report.ObservedStart).Seconds()
		if dur > 0 {
			return dur
		}
	}
	if report.TotalStatements > 0 || report.RecordsInWindow > 0 {
		return 1
	}
	return 0
}

func qpsForCount(count int, seconds float64) float64 {
	if count == 0 || seconds <= 0 {
		return 0
	}
	return float64(count) / seconds
}

func makeFingerprintDelta(aItem, bItem FingerprintSummary, durA, durB float64, status string) FingerprintDelta {
	normalized := bItem.NormalizedQuery
	statementType := bItem.StatementType
	rules := bItem.Rules
	fingerprint := bItem.Fingerprint
	if fingerprint == "" {
		fingerprint = aItem.Fingerprint
	}
	if normalized == "" {
		normalized = aItem.NormalizedQuery
	}
	if statementType == "" {
		statementType = aItem.StatementType
	}
	if len(rules) == 0 {
		rules = aItem.Rules
	}

	qpsA := qpsForCount(aItem.Count, durA)
	qpsB := qpsForCount(bItem.Count, durB)
	return FingerprintDelta{
		Fingerprint:     fingerprint,
		StatementType:   statementType,
		NormalizedQuery: normalized,
		Status:          status,
		CountA:          aItem.Count,
		CountB:          bItem.Count,
		CountDelta:      bItem.Count - aItem.Count,
		QPSA:            qpsA,
		QPSB:            qpsB,
		QPSDelta:        qpsB - qpsA,
		Rules:           append([]string(nil), rules...),
	}
}

func sortFingerprintDeltas(items []FingerprintDelta) {
	sort.Slice(items, func(i, j int) bool {
		absI := items[i].CountDelta
		if absI < 0 {
			absI = -absI
		}
		absJ := items[j].CountDelta
		if absJ < 0 {
			absJ = -absJ
		}
		if absI != absJ {
			return absI > absJ
		}
		return items[i].Fingerprint < items[j].Fingerprint
	})
}

func sortRuleDeltas(items []RuleDelta) {
	sort.Slice(items, func(i, j int) bool {
		absI := items[i].CountDelta
		if absI < 0 {
			absI = -absI
		}
		absJ := items[j].CountDelta
		if absJ < 0 {
			absJ = -absJ
		}
		if absI != absJ {
			return absI > absJ
		}
		return items[i].Rule < items[j].Rule
	})
}
