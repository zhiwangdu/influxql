package analyzer

import (
	"strings"
	"time"

	influxql "github.com/influxdata/influxql"
)

func normalizeStatementInPlace(stmt influxql.Statement) {
	switch s := stmt.(type) {
	case *influxql.SelectStatement:
		normalizeSelectStatement(s)
	case *influxql.ExplainStatement:
		if s.Statement != nil {
			normalizeSelectStatement(s.Statement)
		}
	case *influxql.DeleteStatement:
		normalizeSource(s.Source)
		s.Condition = normalizeExpr(s.Condition)
	case *influxql.ShowSeriesStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.DropSeriesStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
	case *influxql.DeleteSeriesStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
	case *influxql.DropShardStatement:
		s.ID = 1
	case *influxql.ShowSeriesCardinalityStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
		normalizeDimensions(s.Dimensions)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.CreateContinuousQueryStatement:
		if s.Source != nil {
			normalizeSelectStatement(s.Source)
		}
		if s.ResampleEvery > 0 {
			s.ResampleEvery = time.Second
		}
		if s.ResampleFor > 0 {
			s.ResampleFor = time.Second
		}
	case *influxql.ShowMeasurementCardinalityStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
		normalizeDimensions(s.Dimensions)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowMeasurementsStatement:
		normalizeSource(s.Source)
		s.Condition = normalizeExpr(s.Condition)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowStatsStatement:
		if s.Module != "" {
			s.Module = normalizedStringPlaceholder
		}
	case *influxql.ShowDiagnosticsStatement:
		if s.Module != "" {
			s.Module = normalizedStringPlaceholder
		}
	case *influxql.CreateSubscriptionStatement:
		for i := range s.Destinations {
			s.Destinations[i] = normalizedStringPlaceholder
		}
	case *influxql.ShowTagKeysStatement:
		normalizeSources(s.Sources)
		s.TagKeyExpr = normalizeExpr(s.TagKeyExpr)
		s.Condition = normalizeExpr(s.Condition)
		normalizeLimitOffset(&s.Limit, &s.Offset)
		normalizeLimitOffset(&s.SLimit, &s.SOffset)
	case *influxql.ShowTagKeyCardinalityStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
		normalizeDimensions(s.Dimensions)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowTagValuesStatement:
		normalizeSources(s.Sources)
		s.TagKeyExpr = normalizeLiteral(s.TagKeyExpr)
		s.Condition = normalizeExpr(s.Condition)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowTagValuesCardinalityStatement:
		normalizeSources(s.Sources)
		s.TagKeyExpr = normalizeLiteral(s.TagKeyExpr)
		s.Condition = normalizeExpr(s.Condition)
		normalizeDimensions(s.Dimensions)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowFieldKeyCardinalityStatement:
		normalizeSources(s.Sources)
		s.Condition = normalizeExpr(s.Condition)
		normalizeDimensions(s.Dimensions)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.ShowFieldKeysStatement:
		normalizeSources(s.Sources)
		normalizeLimitOffset(&s.Limit, &s.Offset)
	case *influxql.CreateDatabaseStatement:
		if s.RetentionPolicyDuration != nil {
			d := time.Second
			s.RetentionPolicyDuration = &d
		}
		if s.RetentionPolicyReplication != nil {
			v := 1
			s.RetentionPolicyReplication = &v
		}
		if s.RetentionPolicyShardGroupDuration > 0 {
			s.RetentionPolicyShardGroupDuration = time.Second
		}
		if s.FutureWriteLimit != nil {
			d := time.Second
			s.FutureWriteLimit = &d
		}
		if s.PastWriteLimit != nil {
			d := time.Second
			s.PastWriteLimit = &d
		}
	case *influxql.CreateRetentionPolicyStatement:
		s.Duration = time.Second
		s.Replication = 1
		if s.ShardGroupDuration > 0 {
			s.ShardGroupDuration = time.Second
		}
		if s.FutureWriteLimit > 0 {
			s.FutureWriteLimit = time.Second
		}
		if s.PastWriteLimit > 0 {
			s.PastWriteLimit = time.Second
		}
	case *influxql.AlterRetentionPolicyStatement:
		if s.Duration != nil {
			d := time.Second
			s.Duration = &d
		}
		if s.Replication != nil {
			v := 1
			s.Replication = &v
		}
		if s.ShardGroupDuration != nil {
			d := time.Second
			s.ShardGroupDuration = &d
		}
		if s.FutureWriteLimit != nil {
			d := time.Second
			s.FutureWriteLimit = &d
		}
		if s.PastWriteLimit != nil {
			d := time.Second
			s.PastWriteLimit = &d
		}
	case *influxql.KillQueryStatement:
		s.QueryID = 1
	}
}

func normalizeSelectStatement(stmt *influxql.SelectStatement) {
	if stmt == nil {
		return
	}
	for _, field := range stmt.Fields {
		field.Expr = normalizeExpr(field.Expr)
	}
	if stmt.Target != nil {
		normalizeMeasurement(stmt.Target.Measurement)
	}
	normalizeSources(stmt.Sources)
	stmt.Condition = normalizeExpr(stmt.Condition)
	normalizeDimensions(stmt.Dimensions)
	normalizeSortFields(stmt.SortFields)
	normalizeLimitOffset(&stmt.Limit, &stmt.Offset)
	normalizeLimitOffset(&stmt.SLimit, &stmt.SOffset)
	if stmt.Fill == influxql.NumberFill {
		stmt.FillValue = 0
	}
}

func normalizeExpr(expr influxql.Expr) influxql.Expr {
	if expr == nil {
		return nil
	}
	return influxql.RewriteExpr(expr, func(e influxql.Expr) influxql.Expr {
		switch v := e.(type) {
		case *influxql.NumberLiteral:
			return &influxql.NumberLiteral{Val: 0}
		case *influxql.IntegerLiteral:
			return &influxql.IntegerLiteral{Val: 0}
		case *influxql.UnsignedLiteral:
			return &influxql.UnsignedLiteral{Val: 0}
		case *influxql.BooleanLiteral:
			return &influxql.BooleanLiteral{Val: false}
		case *influxql.StringLiteral:
			if v.IsTimeLiteral() {
				return &influxql.StringLiteral{Val: "1970-01-01T00:00:00Z"}
			}
			return &influxql.StringLiteral{Val: normalizedStringPlaceholder}
		case *influxql.TimeLiteral:
			return &influxql.TimeLiteral{Val: time.Unix(0, 0).UTC()}
		case *influxql.DurationLiteral:
			return &influxql.DurationLiteral{Val: time.Second}
		case *influxql.RegexLiteral:
			return &influxql.RegexLiteral{Val: normalizedRegex}
		case *influxql.ListLiteral:
			return &influxql.ListLiteral{Vals: []string{normalizedIdentPlaceholder}}
		case *influxql.BoundParameter:
			return &influxql.BoundParameter{Name: "param"}
		default:
			return e
		}
	})
}

func normalizeLiteral(lit influxql.Literal) influxql.Literal {
	switch v := lit.(type) {
	case *influxql.StringLiteral:
		return &influxql.StringLiteral{Val: normalizedIdentPlaceholder}
	case *influxql.ListLiteral:
		return &influxql.ListLiteral{Vals: []string{normalizedIdentPlaceholder}}
	case *influxql.RegexLiteral:
		return &influxql.RegexLiteral{Val: normalizedRegex}
	default:
		return v
	}
}

func normalizeSources(sources influxql.Sources) {
	for _, source := range sources {
		normalizeSource(source)
	}
}

func normalizeSource(source influxql.Source) {
	switch s := source.(type) {
	case *influxql.Measurement:
		normalizeMeasurement(s)
	case *influxql.SubQuery:
		if s.Statement != nil {
			normalizeSelectStatement(s.Statement)
		}
	}
}

func normalizeMeasurement(m *influxql.Measurement) {
	if m == nil {
		return
	}
	if m.Regex != nil {
		m.Regex = &influxql.RegexLiteral{Val: normalizedRegex}
	}
}

func normalizeDimensions(dimensions influxql.Dimensions) {
	for _, dim := range dimensions {
		dim.Expr = normalizeExpr(dim.Expr)
	}
}

func normalizeSortFields(fields influxql.SortFields) {
	for _, field := range fields {
		field.Name = strings.ToLower(field.Name)
	}
}

func normalizeLimitOffset(limit, offset *int) {
	if limit != nil && *limit > 0 {
		*limit = 1
	}
	if offset != nil && *offset > 0 {
		*offset = 1
	}
}

func extractFeatures(stmt influxql.Statement) FeatureSet {
	features := FeatureSet{
		StatementType:        statementType(stmt),
		IsMetaQuery:          isMetaStatement(stmt),
		IsWriteOrDestructive: isWriteOrDestructive(stmt),
	}

	switch s := stmt.(type) {
	case *influxql.SelectStatement:
		features.HasWildcard = s.HasWildcard()
		features.Limit = s.Limit
		features.SLimit = s.SLimit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.IsSelectInto = s.Target != nil
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ExplainStatement:
		if s.Statement != nil {
			inner := extractFeatures(s.Statement)
			features.HasWildcard = inner.HasWildcard
			features.Limit = inner.Limit
			features.SLimit = inner.SLimit
			features.GroupByCount = inner.GroupByCount
			features.NonTimeGroupByCount = inner.NonTimeGroupByCount
			features.IsSelectInto = inner.IsSelectInto
			features.HasTimeFilter = inner.HasTimeFilter
			features.TimeRangeExtracted = inner.TimeRangeExtracted
		}
	case *influxql.ShowSeriesStatement:
		features.Limit = s.Limit
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.DeleteStatement:
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.DropSeriesStatement:
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.DeleteSeriesStatement:
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowSeriesCardinalityStatement:
		features.Limit = s.Limit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.CreateContinuousQueryStatement:
		if s.Source != nil {
			inner := extractFeatures(s.Source)
			features.HasWildcard = inner.HasWildcard
			features.GroupByCount = inner.GroupByCount
			features.NonTimeGroupByCount = inner.NonTimeGroupByCount
			features.HasTimeFilter = inner.HasTimeFilter
			features.TimeRangeExtracted = inner.TimeRangeExtracted
			features.IsSelectInto = inner.IsSelectInto
		}
	case *influxql.ShowMeasurementCardinalityStatement:
		features.Limit = s.Limit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowMeasurementsStatement:
		features.Limit = s.Limit
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowTagKeysStatement:
		features.Limit = s.Limit
		features.SLimit = s.SLimit
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowTagKeyCardinalityStatement:
		features.Limit = s.Limit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowTagValuesStatement:
		features.Limit = s.Limit
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowTagValuesCardinalityStatement:
		features.Limit = s.Limit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowFieldKeyCardinalityStatement:
		features.Limit = s.Limit
		features.GroupByCount, features.NonTimeGroupByCount = dimensionCounts(s.Dimensions)
		features.HasTimeFilter, features.TimeRangeExtracted = extractTimeFilter(s.Condition)
	case *influxql.ShowFieldKeysStatement:
		features.Limit = s.Limit
	}

	features.HasRegex = detectRegex(stmt)
	if !features.HasWildcard {
		features.HasWildcard = detectWildcard(stmt)
	}
	if features.IsSelectInto {
		features.IsWriteOrDestructive = true
	}
	return features
}

func extractTimeFilter(expr influxql.Expr) (bool, bool) {
	if expr == nil {
		return false, false
	}
	_, tr, err := influxql.ConditionExpr(expr, nil)
	if err != nil {
		return false, false
	}
	return !tr.IsZero(), !tr.IsZero()
}

func dimensionCounts(dimensions influxql.Dimensions) (int, int) {
	if len(dimensions) == 0 {
		return 0, 0
	}
	total := len(dimensions)
	nonTime := 0
	for _, dim := range dimensions {
		call, ok := dim.Expr.(*influxql.Call)
		if ok && strings.EqualFold(call.Name, "time") {
			continue
		}
		nonTime++
	}
	return total, nonTime
}

func detectRegex(stmt influxql.Statement) bool {
	found := false
	influxql.WalkFunc(stmt, func(node influxql.Node) {
		if found {
			return
		}
		switch n := node.(type) {
		case *influxql.RegexLiteral:
			found = true
		case *influxql.BinaryExpr:
			if n.Op == influxql.EQREGEX || n.Op == influxql.NEQREGEX {
				found = true
			}
		}
	})
	if found {
		return true
	}

	switch s := stmt.(type) {
	case *influxql.SelectStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.DeleteStatement:
		return sourceHasRegex(s.Source)
	case *influxql.ShowSeriesStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.DropSeriesStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.DeleteSeriesStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ShowSeriesCardinalityStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ExplainStatement:
		return s.Statement != nil && detectRegex(s.Statement)
	case *influxql.CreateContinuousQueryStatement:
		return s.Source != nil && detectRegex(s.Source)
	case *influxql.ShowMeasurementCardinalityStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ShowMeasurementsStatement:
		return sourceHasRegex(s.Source)
	case *influxql.ShowTagKeysStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ShowTagKeyCardinalityStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ShowTagValuesStatement:
		return sourcesHaveRegex(s.Sources) || literalHasRegex(s.TagKeyExpr)
	case *influxql.ShowTagValuesCardinalityStatement:
		return sourcesHaveRegex(s.Sources) || literalHasRegex(s.TagKeyExpr)
	case *influxql.ShowFieldKeyCardinalityStatement:
		return sourcesHaveRegex(s.Sources)
	case *influxql.ShowFieldKeysStatement:
		return sourcesHaveRegex(s.Sources)
	}
	return false
}

func detectWildcard(stmt influxql.Statement) bool {
	switch s := stmt.(type) {
	case *influxql.SelectStatement:
		return s.HasWildcard()
	case *influxql.ExplainStatement:
		return s.Statement != nil && s.Statement.HasWildcard()
	case *influxql.ShowMeasurementsStatement:
		return s.WildcardDatabase || s.WildcardRetentionPolicy
	default:
		return false
	}
}

func sourcesHaveRegex(sources influxql.Sources) bool {
	for _, source := range sources {
		if sourceHasRegex(source) {
			return true
		}
	}
	return false
}

func sourceHasRegex(source influxql.Source) bool {
	switch s := source.(type) {
	case *influxql.Measurement:
		return s != nil && s.Regex != nil
	case *influxql.SubQuery:
		return s != nil && s.Statement != nil && detectRegex(s.Statement)
	default:
		return false
	}
}

func literalHasRegex(lit influxql.Literal) bool {
	_, ok := lit.(*influxql.RegexLiteral)
	return ok
}

func statementType(stmt influxql.Statement) string {
	switch stmt.(type) {
	case *influxql.SelectStatement:
		return "SELECT"
	case *influxql.ExplainStatement:
		return "EXPLAIN"
	case *influxql.DeleteStatement:
		return "DELETE"
	case *influxql.ShowSeriesStatement:
		return "SHOW SERIES"
	case *influxql.DropSeriesStatement:
		return "DROP SERIES"
	case *influxql.DeleteSeriesStatement:
		return "DELETE SERIES"
	case *influxql.DropShardStatement:
		return "DROP SHARD"
	case *influxql.ShowSeriesCardinalityStatement:
		return "SHOW SERIES CARDINALITY"
	case *influxql.ShowContinuousQueriesStatement:
		return "SHOW CONTINUOUS QUERIES"
	case *influxql.ShowGrantsForUserStatement:
		return "SHOW GRANTS"
	case *influxql.ShowDatabasesStatement:
		return "SHOW DATABASES"
	case *influxql.CreateContinuousQueryStatement:
		return "CREATE CONTINUOUS QUERY"
	case *influxql.DropContinuousQueryStatement:
		return "DROP CONTINUOUS QUERY"
	case *influxql.ShowMeasurementCardinalityStatement:
		return "SHOW MEASUREMENT CARDINALITY"
	case *influxql.ShowMeasurementsStatement:
		return "SHOW MEASUREMENTS"
	case *influxql.DropMeasurementStatement:
		return "DROP MEASUREMENT"
	case *influxql.ShowQueriesStatement:
		return "SHOW QUERIES"
	case *influxql.ShowRetentionPoliciesStatement:
		return "SHOW RETENTION POLICIES"
	case *influxql.ShowStatsStatement:
		return "SHOW STATS"
	case *influxql.ShowShardGroupsStatement:
		return "SHOW SHARD GROUPS"
	case *influxql.ShowShardsStatement:
		return "SHOW SHARDS"
	case *influxql.ShowDiagnosticsStatement:
		return "SHOW DIAGNOSTICS"
	case *influxql.CreateSubscriptionStatement:
		return "CREATE SUBSCRIPTION"
	case *influxql.DropSubscriptionStatement:
		return "DROP SUBSCRIPTION"
	case *influxql.ShowSubscriptionsStatement:
		return "SHOW SUBSCRIPTIONS"
	case *influxql.ShowTagKeysStatement:
		return "SHOW TAG KEYS"
	case *influxql.ShowTagKeyCardinalityStatement:
		return "SHOW TAG KEY CARDINALITY"
	case *influxql.ShowTagValuesStatement:
		return "SHOW TAG VALUES"
	case *influxql.ShowTagValuesCardinalityStatement:
		return "SHOW TAG VALUES CARDINALITY"
	case *influxql.ShowUsersStatement:
		return "SHOW USERS"
	case *influxql.ShowFieldKeyCardinalityStatement:
		return "SHOW FIELD KEY CARDINALITY"
	case *influxql.ShowFieldKeysStatement:
		return "SHOW FIELD KEYS"
	case *influxql.CreateDatabaseStatement:
		return "CREATE DATABASE"
	case *influxql.DropDatabaseStatement:
		return "DROP DATABASE"
	case *influxql.DropRetentionPolicyStatement:
		return "DROP RETENTION POLICY"
	case *influxql.CreateUserStatement:
		return "CREATE USER"
	case *influxql.DropUserStatement:
		return "DROP USER"
	case *influxql.GrantStatement:
		return "GRANT"
	case *influxql.GrantAdminStatement:
		return "GRANT ADMIN"
	case *influxql.KillQueryStatement:
		return "KILL QUERY"
	case *influxql.SetPasswordUserStatement:
		return "SET PASSWORD"
	case *influxql.RevokeStatement:
		return "REVOKE"
	case *influxql.RevokeAdminStatement:
		return "REVOKE ADMIN"
	case *influxql.CreateRetentionPolicyStatement:
		return "CREATE RETENTION POLICY"
	case *influxql.AlterRetentionPolicyStatement:
		return "ALTER RETENTION POLICY"
	default:
		return "UNKNOWN"
	}
}

func isMetaStatement(stmt influxql.Statement) bool {
	switch stmt.(type) {
	case *influxql.ExplainStatement,
		*influxql.ShowSeriesStatement,
		*influxql.ShowSeriesCardinalityStatement,
		*influxql.ShowContinuousQueriesStatement,
		*influxql.ShowGrantsForUserStatement,
		*influxql.ShowDatabasesStatement,
		*influxql.ShowMeasurementCardinalityStatement,
		*influxql.ShowMeasurementsStatement,
		*influxql.ShowQueriesStatement,
		*influxql.ShowRetentionPoliciesStatement,
		*influxql.ShowStatsStatement,
		*influxql.ShowShardGroupsStatement,
		*influxql.ShowShardsStatement,
		*influxql.ShowDiagnosticsStatement,
		*influxql.ShowSubscriptionsStatement,
		*influxql.ShowTagKeysStatement,
		*influxql.ShowTagKeyCardinalityStatement,
		*influxql.ShowTagValuesStatement,
		*influxql.ShowTagValuesCardinalityStatement,
		*influxql.ShowUsersStatement,
		*influxql.ShowFieldKeyCardinalityStatement,
		*influxql.ShowFieldKeysStatement:
		return true
	default:
		return false
	}
}

func isWriteOrDestructive(stmt influxql.Statement) bool {
	switch stmt.(type) {
	case *influxql.DeleteStatement,
		*influxql.DropSeriesStatement,
		*influxql.DeleteSeriesStatement,
		*influxql.DropShardStatement,
		*influxql.DropContinuousQueryStatement,
		*influxql.DropMeasurementStatement,
		*influxql.DropSubscriptionStatement,
		*influxql.DropDatabaseStatement,
		*influxql.DropRetentionPolicyStatement:
		return true
	default:
		return false
	}
}
