package analyzer

func matchRules(features FeatureSet, cfg RuleConfig) []RuleMatch {
	matches := make([]RuleMatch, 0, 4)

	if ruleEnabled(cfg, RuleNoTimeFilter) && features.StatementType == "SELECT" && !features.HasTimeFilter {
		matches = append(matches, RuleMatch{Name: RuleNoTimeFilter, Reason: "select statement has no explicit time predicate"})
	}
	if ruleEnabled(cfg, RuleHasRegex) && features.HasRegex {
		matches = append(matches, RuleMatch{Name: RuleHasRegex, Reason: "query uses regex matching"})
	}
	if ruleEnabled(cfg, RuleHasWildcard) && features.HasWildcard {
		matches = append(matches, RuleMatch{Name: RuleHasWildcard, Reason: "query uses wildcard selection or grouping"})
	}
	if ruleEnabled(cfg, RuleLargeLimit) && (features.Limit >= cfg.LargeLimitThreshold || features.SLimit >= cfg.LargeLimitThreshold) {
		matches = append(matches, RuleMatch{Name: RuleLargeLimit, Reason: "query limit exceeds configured threshold"})
	}
	if ruleEnabled(cfg, RuleGroupByHighCardinality) && features.NonTimeGroupByCount >= cfg.GroupByTagThreshold {
		matches = append(matches, RuleMatch{Name: RuleGroupByHighCardinality, Reason: "query groups by multiple non-time dimensions"})
	}
	if ruleEnabled(cfg, RuleMetaQuery) && features.IsMetaQuery {
		matches = append(matches, RuleMatch{Name: RuleMetaQuery, Reason: "query is a metadata or explain statement"})
	}
	if ruleEnabled(cfg, RuleWriteOrDestructive) && features.IsWriteOrDestructive {
		matches = append(matches, RuleMatch{Name: RuleWriteOrDestructive, Reason: "query writes data or performs destructive changes"})
	}

	return matches
}

func ruleEnabled(cfg RuleConfig, rule string) bool {
	if cfg.Enabled == nil {
		return true
	}
	enabled, ok := cfg.Enabled[rule]
	if !ok {
		return true
	}
	return enabled
}
