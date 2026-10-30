package store

import (
	"slices"
	"strings"
	"time"
)

// CallQuery filters, sorts and pages ListCalls.
type CallQuery struct {
	Agent  string    // "" lists every agent's calls
	Since  time.Time // zero means no time limit
	Search string    // case-insensitive substring of ID, agent, phone, status, sentiment or category
	IDs    []string  // when set, only these calls
	Sort   string    // one of SortKeys; "" means initiationTimestamp
	Desc   bool
	Limit  int // 0 means no limit
	Offset int
}

// SortKeys maps the API's sort names to their Postgres columns.
var SortKeys = map[string]string{
	"id":                   "id",
	"agent":                "agent",
	"initiationTimestamp":  "initiation_timestamp",
	"callerPhoneNumber":    "caller_phone_number",
	"status":               "status",
	"callerSentiment":      "caller_avg_sentiment",
	"callerSentimentTrend": "caller_sentiment_trend",
	"duration":             "duration",
	"alertCount":           "alert_count",
}

const DefaultSort = "initiationTimestamp"

func (q CallQuery) sortKey() string {
	if _, ok := SortKeys[q.Sort]; ok {
		return q.Sort
	}
	return DefaultSort
}

// matches applies every filter except paging; Memory uses it, Postgres uses SQL.
func (q CallQuery) matches(c Call) bool {
	if q.Agent != "" && c.Agent != q.Agent {
		return false
	}
	if !q.Since.IsZero() {
		started, err := time.Parse(time.RFC3339, c.InitiationTimestamp)
		if err != nil || started.Before(q.Since) {
			return false
		}
	}
	if len(q.IDs) > 0 && !slices.Contains(q.IDs, c.ID) {
		return false
	}
	if q.Search == "" {
		return true
	}
	needle := strings.ToLower(q.Search)
	fields := append([]string{c.ID, c.Agent, c.CallerPhoneNumber, c.Status, c.CallerSentiment}, c.Categories...)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}

// escapeLike makes s match literally inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
