package analyzer

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	influxql "github.com/influxdata/influxql"
)

const (
	realtimeStatusRealtime    = "realtime"
	realtimeStatusNonRealtime = "non_realtime"
	realtimeStatusUnknown     = "unknown"
)

type queryAnalysis struct {
	Results        []NormalizeResult
	RealtimeChecks []realtimeCheck
}

type realtimeCheck struct {
	SelectLike bool
	Condition  influxql.Expr
}

type realtimeEvaluation struct {
	Status    string
	Reason    string
	TimeRange influxql.TimeRange
}

func extractRealtimeCheck(stmt influxql.Statement) realtimeCheck {
	switch s := stmt.(type) {
	case *influxql.SelectStatement:
		return realtimeCheck{SelectLike: true, Condition: influxql.CloneExpr(s.Condition)}
	case *influxql.ExplainStatement:
		if s.Statement != nil {
			return realtimeCheck{SelectLike: true, Condition: influxql.CloneExpr(s.Statement.Condition)}
		}
	case *influxql.CreateContinuousQueryStatement:
		if s.Source != nil {
			return realtimeCheck{SelectLike: true, Condition: influxql.CloneExpr(s.Source.Condition)}
		}
	}
	return realtimeCheck{}
}

func realtimeCheckAt(checks []realtimeCheck, idx int) realtimeCheck {
	if idx < 0 || idx >= len(checks) {
		return realtimeCheck{}
	}
	return checks[idx]
}

func (a *Analyzer) evaluateRealtime(check realtimeCheck, logTime time.Time, query string) realtimeEvaluation {
	eval := evaluateRealtimeQuery(check, logTime, time.Duration(a.cfg.RealtimeQuery.ThresholdSeconds)*time.Second)
	if !check.SelectLike {
		return eval
	}

	a.report.RealtimeQuery.Total++
	switch eval.Status {
	case realtimeStatusRealtime:
		a.report.RealtimeQuery.Realtime++
	case realtimeStatusNonRealtime:
		a.report.RealtimeQuery.NonRealtime++
		a.addNonRealtimeLogTimeBucket(logTime)
		a.appendRealtimeSample(&a.report.RealtimeQuery.SampleNonRealtime, query, eval, logTime)
	case realtimeStatusUnknown:
		a.report.RealtimeQuery.Unknown++
		a.appendRealtimeSample(&a.report.RealtimeQuery.SampleUnknown, query, eval, logTime)
	}
	return eval
}

func evaluateRealtimeQuery(check realtimeCheck, logTime time.Time, threshold time.Duration) realtimeEvaluation {
	if !check.SelectLike {
		return realtimeEvaluation{Status: realtimeStatusUnknown, Reason: "statement is not a select-like query"}
	}
	if logTime.IsZero() {
		return realtimeEvaluation{Status: realtimeStatusUnknown, Reason: "log time is empty"}
	}
	if check.Condition == nil {
		return realtimeEvaluation{Status: realtimeStatusUnknown, Reason: "query has no where time predicate"}
	}

	timeRange, found, err := extractRealtimeTimeRange(check.Condition, logTime)
	if err != nil {
		return realtimeEvaluation{Status: realtimeStatusUnknown, Reason: err.Error(), TimeRange: timeRange}
	}
	if !found || timeRange.IsZero() {
		return realtimeEvaluation{Status: realtimeStatusUnknown, Reason: "query has no extractable where time predicate"}
	}
	if timeRange.Min.IsZero() {
		return realtimeEvaluation{Status: realtimeStatusNonRealtime, Reason: "where time predicate has no lower bound", TimeRange: timeRange}
	}

	age := logTime.Sub(timeRange.Min)
	if age < 0 {
		return realtimeEvaluation{Status: realtimeStatusNonRealtime, Reason: "where time lower bound is after log time", TimeRange: timeRange}
	}
	if sameLocalDay(logTime, timeRange.Min) {
		return realtimeEvaluation{Status: realtimeStatusRealtime, Reason: "where time lower bound is on the log day", TimeRange: timeRange}
	}
	if age <= threshold {
		return realtimeEvaluation{Status: realtimeStatusRealtime, Reason: "where time lower bound is within threshold", TimeRange: timeRange}
	}
	return realtimeEvaluation{Status: realtimeStatusNonRealtime, Reason: "where time lower bound is older than threshold", TimeRange: timeRange}
}

func extractRealtimeTimeRange(expr influxql.Expr, logTime time.Time) (influxql.TimeRange, bool, error) {
	if expr == nil {
		return influxql.TimeRange{}, false, nil
	}

	switch e := expr.(type) {
	case *influxql.BinaryExpr:
		if e.Op == influxql.AND {
			lhs, lhsFound, err := extractRealtimeTimeRange(e.LHS, logTime)
			if err != nil {
				return influxql.TimeRange{}, false, err
			}
			rhs, rhsFound, err := extractRealtimeTimeRange(e.RHS, logTime)
			if err != nil {
				return influxql.TimeRange{}, false, err
			}
			if lhsFound && rhsFound {
				return lhs.Intersect(rhs), true, nil
			}
			if lhsFound {
				return lhs, true, nil
			}
			return rhs, rhsFound, nil
		}
		if e.Op == influxql.OR {
			_, lhsFound, _ := extractRealtimeTimeRange(e.LHS, logTime)
			_, rhsFound, _ := extractRealtimeTimeRange(e.RHS, logTime)
			if lhsFound || rhsFound {
				return influxql.TimeRange{}, true, fmt.Errorf("where time predicate under OR is not supported")
			}
			return influxql.TimeRange{}, false, nil
		}
		return extractRealtimeComparison(e, logTime)
	case *influxql.ParenExpr:
		return extractRealtimeTimeRange(e.Expr, logTime)
	default:
		return influxql.TimeRange{}, false, nil
	}
}

func extractRealtimeComparison(expr *influxql.BinaryExpr, logTime time.Time) (influxql.TimeRange, bool, error) {
	if lhs, ok := expr.LHS.(*influxql.VarRef); ok && strings.EqualFold(lhs.Val, "time") {
		value, err := realtimeTimeValue(expr.RHS, logTime)
		if err != nil {
			return influxql.TimeRange{}, true, err
		}
		return realtimeRangeForOp(expr.Op, value)
	}
	if rhs, ok := expr.RHS.(*influxql.VarRef); ok && strings.EqualFold(rhs.Val, "time") {
		value, err := realtimeTimeValue(expr.LHS, logTime)
		if err != nil {
			return influxql.TimeRange{}, true, err
		}
		return realtimeRangeForOp(reverseComparisonOp(expr.Op), value)
	}
	return influxql.TimeRange{}, false, nil
}

func realtimeTimeValue(expr influxql.Expr, logTime time.Time) (time.Time, error) {
	valuer := &influxql.NowValuer{Now: logTime, Location: logTime.Location()}
	reduced := influxql.Reduce(expr, valuer)

	switch lit := reduced.(type) {
	case *influxql.TimeLiteral:
		return lit.Val, nil
	case *influxql.StringLiteral:
		return parseRealtimeTimeString(lit.Val, logTime.Location())
	case *influxql.IntegerLiteral:
		return inferUnixTimestamp(float64(lit.Val)), nil
	case *influxql.UnsignedLiteral:
		if lit.Val > uint64(1<<63-1) {
			return time.Time{}, fmt.Errorf("time timestamp %d overflows int64", lit.Val)
		}
		return inferUnixTimestamp(float64(lit.Val)), nil
	case *influxql.NumberLiteral:
		return inferUnixTimestamp(lit.Val), nil
	default:
		return time.Time{}, fmt.Errorf("invalid where time value %T", reduced)
	}
}

func parseRealtimeTimeString(value string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, influxql.DateTimeFormat, influxql.DateFormat} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid where time string %q", value)
}

func inferUnixTimestamp(value float64) time.Time {
	abs := math.Abs(value)
	switch {
	case abs >= 1e17:
		return time.Unix(0, int64(value)).UTC()
	case abs >= 1e14:
		return time.Unix(0, int64(value*1e3)).UTC()
	case abs >= 1e11:
		sec, frac := math.Modf(value / 1e3)
		return time.Unix(int64(sec), int64(frac*1e9)).UTC()
	default:
		sec, frac := math.Modf(value)
		return time.Unix(int64(sec), int64(frac*1e9)).UTC()
	}
}

func realtimeRangeForOp(op influxql.Token, value time.Time) (influxql.TimeRange, bool, error) {
	timeRange := influxql.TimeRange{}
	switch op {
	case influxql.GT:
		timeRange.Min = value.Add(time.Nanosecond)
	case influxql.GTE:
		timeRange.Min = value
	case influxql.LT:
		timeRange.Max = value.Add(-time.Nanosecond)
	case influxql.LTE:
		timeRange.Max = value
	case influxql.EQ:
		timeRange.Min = value
		timeRange.Max = value
	default:
		return influxql.TimeRange{}, true, fmt.Errorf("invalid where time comparison operator: %s", op)
	}
	return timeRange, true, nil
}

func reverseComparisonOp(op influxql.Token) influxql.Token {
	switch op {
	case influxql.GT:
		return influxql.LT
	case influxql.GTE:
		return influxql.LTE
	case influxql.LT:
		return influxql.GT
	case influxql.LTE:
		return influxql.GTE
	default:
		return op
	}
}

func sameLocalDay(a, b time.Time) bool {
	loc := a.Location()
	if loc == nil {
		loc = time.UTC
	}
	a = a.In(loc)
	b = b.In(loc)
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (a *Analyzer) appendRealtimeSample(samples *[]RealtimeQuerySample, query string, eval realtimeEvaluation, logTime time.Time) {
	if query == "" || len(*samples) >= a.cfg.DetailLimit {
		return
	}
	var ts *time.Time
	if !logTime.IsZero() {
		copyTime := logTime
		ts = &copyTime
	}
	*samples = append(*samples, RealtimeQuerySample{
		Query:     query,
		Reason:    eval.Reason,
		LogTime:   ts,
		TimeRange: realtimeTimeRangeSummary(eval.TimeRange),
	})
}

func realtimeTimeRangeSummary(timeRange influxql.TimeRange) *RealtimeTimeRangeSummary {
	if timeRange.IsZero() {
		return nil
	}
	summary := &RealtimeTimeRangeSummary{}
	if !timeRange.Min.IsZero() {
		min := timeRange.Min
		summary.Min = &min
	}
	if !timeRange.Max.IsZero() {
		max := timeRange.Max
		summary.Max = &max
	}
	if !timeRange.Min.IsZero() && !timeRange.Max.IsZero() && !timeRange.Max.Before(timeRange.Min) {
		seconds := timeRange.Max.Sub(timeRange.Min).Seconds()
		summary.WindowSeconds = &seconds
	}
	return summary
}

func (a *Analyzer) mergeRealtimeQuery(other RealtimeQuerySummary) {
	a.report.RealtimeQuery.Total += other.Total
	a.report.RealtimeQuery.Realtime += other.Realtime
	a.report.RealtimeQuery.NonRealtime += other.NonRealtime
	a.report.RealtimeQuery.Unknown += other.Unknown
	for _, sample := range other.SampleNonRealtime {
		appendRealtimeQuerySample(&a.report.RealtimeQuery.SampleNonRealtime, sample, a.cfg.DetailLimit)
	}
	for _, sample := range other.SampleUnknown {
		appendRealtimeQuerySample(&a.report.RealtimeQuery.SampleUnknown, sample, a.cfg.DetailLimit)
	}
}

func appendRealtimeQuerySample(samples *[]RealtimeQuerySample, sample RealtimeQuerySample, limit int) {
	if sample.Query == "" || len(*samples) >= limit {
		return
	}
	*samples = append(*samples, sample)
}

func (a *Analyzer) addNonRealtimeLogTimeBucket(logTime time.Time) {
	if logTime.IsZero() {
		return
	}
	bucketStart := logTime.UTC().Truncate(time.Hour)
	bucket := a.nonRealtimeLogTimeBuckets[bucketStart]
	if bucket == nil {
		bucket = &RealtimeLogTimeBucketSummary{
			BucketStart: bucketStart,
			BucketEnd:   bucketStart.Add(time.Hour),
		}
		a.nonRealtimeLogTimeBuckets[bucketStart] = bucket
	}
	bucket.Count++
}

func sortedRealtimeLogTimeBuckets(src map[time.Time]*RealtimeLogTimeBucketSummary) []RealtimeLogTimeBucketSummary {
	if len(src) == 0 {
		return nil
	}
	out := make([]RealtimeLogTimeBucketSummary, 0, len(src))
	for _, bucket := range src {
		out = append(out, *bucket)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].BucketStart.Before(out[j].BucketStart)
	})
	return out
}

func (a *Analyzer) mergeNonRealtimeLogTimeBuckets(other *Analyzer) {
	for start, otherBucket := range other.nonRealtimeLogTimeBuckets {
		bucket := a.nonRealtimeLogTimeBuckets[start]
		if bucket == nil {
			copyBucket := *otherBucket
			a.nonRealtimeLogTimeBuckets[start] = &copyBucket
			continue
		}
		bucket.Count += otherBucket.Count
	}
}
