package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type Summary struct {
	Text string `json:"text"`
}

type SentimentPoint struct {
	Time   string  `json:"time"`
	Agent  float64 `json:"agent"`
	Caller float64 `json:"caller"`
}

type Segment struct {
	ID             int      `json:"id"` // sequence number within the call
	Speaker        string   `json:"speaker"`
	TimeStart      string   `json:"timeStart"`
	TimeEnd        string   `json:"timeEnd"`
	Text           string   `json:"text"`
	Sentiment      *string  `json:"sentiment"`
	SentimentScore *float64 `json:"sentimentScore,omitempty"`
	Emoji          *string  `json:"emoji,omitempty"`
	PII            []string `json:"pii,omitempty"`
	// Insight marks a line the live analysis picked out: InsightIssue,
	// InsightAction or InsightOutcome; empty for other lines
	Insight string `json:"insight,omitempty"`
	// The words of the line that carry the insight, for underlining; worked
	// out when the line is sent, not stored
	InsightText string `json:"insightText,omitempty"`
	// Assist holds a knowledge-base answer's details on an AGENT_ASSISTANT line
	Assist *SegmentAssist `json:"assist,omitempty"`
}

// Insight labels on transcript lines
const (
	InsightIssue   = "issue"   // the caller's reason for calling
	InsightAction  = "action"  // something the agent commits to do
	InsightOutcome = "outcome" // something the agent did or confirmed
)

// SegmentAssist is knowledge-base guidance shown inline in the transcript.
type SegmentAssist struct {
	Query string `json:"query"` // what the customer said
	// What the agent can say to the customer, and the steps to take
	Say     string         `json:"say,omitempty"`
	Steps   []string       `json:"steps,omitempty"`
	Model   string         `json:"model,omitempty"`
	Sources []AssistSource `json:"sources,omitempty"`
	// The model is still writing the reply; the policy's own lines show until then
	Pending bool `json:"pending,omitempty"`
}

type AssistSource struct {
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	File    string `json:"file,omitempty"`
	Excerpt string `json:"excerpt,omitempty"` // the passage the answer is based on
}

// segmentMeta is the JSON stored in transcript_segments.meta.
type segmentMeta struct {
	Insight string         `json:"insight,omitempty"`
	Assist  *SegmentAssist `json:"assist,omitempty"`
}

// metaJSON returns the segment's meta column value, or nil when it has none.
func metaJSON(seg Segment) (*string, error) {
	if seg.Insight == "" && seg.Assist == nil {
		return nil, nil
	}
	b, err := json.Marshal(segmentMeta{Insight: seg.Insight, Assist: seg.Assist})
	if err != nil {
		return nil, err
	}
	m := string(b)
	return &m, nil
}

type Call struct {
	ID                   string           `json:"id"`
	Agent                string           `json:"agent"`
	InitiationTimestamp  string           `json:"initiationTimestamp"`
	UpdatedAt            string           `json:"updatedAt"`
	CallerPhoneNumber    string           `json:"callerPhoneNumber"`
	SystemPhoneNumber    string           `json:"systemPhoneNumber"`
	Status               string           `json:"status"`
	CallerSentiment      string           `json:"callerSentiment"`
	CallerSentimentTrend string           `json:"callerSentimentTrend"`
	CallerAvgSentiment   float64          `json:"callerAvgSentiment"`
	AgentAvgSentiment    float64          `json:"agentAvgSentiment"`
	AgentSentimentTrend  string           `json:"agentSentimentTrend"`
	Duration             string           `json:"duration"`
	Categories           []string         `json:"categories"`
	AlertCount           int              `json:"alertCount"`
	Summary              *Summary         `json:"summary"`
	SentimentHistory     []SentimentPoint `json:"sentimentHistory"`
	Analysis             *CallAnalysis    `json:"analysis"`
	// Set by the API when the call's audio was recorded; not stored
	HasRecording bool      `json:"hasRecording,omitempty"`
	Transcript   []Segment `json:"transcript,omitempty"`
}

var callColumns = `id, agent, ` + ts("initiation_timestamp") + `, ` + ts("updated_at") + `,
	caller_phone_number, system_phone_number, status, caller_sentiment, caller_sentiment_trend,
	caller_avg_sentiment, agent_avg_sentiment, agent_sentiment_trend, duration, categories,
	alert_count, summary, sentiment_history, analysis`

// scanCall reads callColumns, then any extra selected columns into extra.
func scanCall(row pgx.Row, extra ...any) (Call, error) {
	var c Call
	var summary *string
	var analysis []byte
	dest := append([]any{&c.ID, &c.Agent, &c.InitiationTimestamp, &c.UpdatedAt,
		&c.CallerPhoneNumber, &c.SystemPhoneNumber, &c.Status, &c.CallerSentiment, &c.CallerSentimentTrend,
		&c.CallerAvgSentiment, &c.AgentAvgSentiment, &c.AgentSentimentTrend, &c.Duration, &c.Categories,
		&c.AlertCount, &summary, &c.SentimentHistory, &analysis}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if summary != nil {
		c.Summary = &Summary{Text: *summary}
	}
	if err == nil && analysis != nil {
		c.Analysis = &CallAnalysis{}
		err = json.Unmarshal(analysis, c.Analysis)
	}
	return c, err
}

// ListCalls returns one page of matching calls, without transcripts, and the
// total number of matches.
func (s *Store) ListCalls(ctx context.Context, q CallQuery) ([]Call, int, error) {
	args := []any{q.Agent}
	where := []string{`($1 = '' OR agent = $1)`}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if !q.Since.IsZero() {
		where = append(where, `initiation_timestamp >= `+arg(q.Since))
	}
	if len(q.IDs) > 0 {
		where = append(where, `id = ANY(`+arg(q.IDs)+`)`)
	}
	if q.Search != "" {
		p := arg("%" + escapeLike(q.Search) + "%")
		where = append(where, `(id ILIKE `+p+` OR agent ILIKE `+p+` OR caller_phone_number ILIKE `+p+
			` OR status ILIKE `+p+` OR caller_sentiment ILIKE `+p+` OR array_to_string(categories, ' ') ILIKE `+p+`)`)
	}
	whereArgs := len(args)
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	// Column names come from the SortKeys allowlist, never from the request
	sql := `SELECT ` + callColumns + `, count(*) OVER () FROM calls WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY ` + SortKeys[q.sortKey()] + ` ` + dir + `, id ` + dir
	if q.Limit > 0 {
		sql += ` LIMIT ` + arg(q.Limit)
	}
	if q.Offset > 0 {
		sql += ` OFFSET ` + arg(q.Offset)
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	calls := []Call{}
	total := 0
	for rows.Next() {
		c, err := scanCall(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		calls = append(calls, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// A page past the end returns no rows, so count separately
	if len(calls) == 0 && q.Offset > 0 {
		countSQL := `SELECT count(*) FROM calls WHERE ` + strings.Join(where, " AND ")
		err = s.pool.QueryRow(ctx, countSQL, args[:whereArgs]...).Scan(&total)
	}
	return calls, total, err
}

func (s *Store) CallCountsByAgent(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT agent, count(*) FROM calls GROUP BY agent`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var agent string
		var n int
		if err := rows.Scan(&agent, &n); err != nil {
			return nil, err
		}
		counts[agent] = n
	}
	return counts, rows.Err()
}

// GetCall returns a call with its full transcript.
func (s *Store) GetCall(ctx context.Context, id string) (Call, error) {
	c, err := scanCall(s.pool.QueryRow(ctx, `SELECT `+callColumns+` FROM calls WHERE id = $1`, id))
	if err != nil {
		return c, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT seq, speaker, time_start, time_end, text, sentiment, sentiment_score, emoji, pii, meta
		FROM transcript_segments WHERE call_id = $1 ORDER BY seq`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	c.Transcript = []Segment{}
	for rows.Next() {
		var seg Segment
		var meta []byte
		if err := rows.Scan(&seg.ID, &seg.Speaker, &seg.TimeStart, &seg.TimeEnd, &seg.Text,
			&seg.Sentiment, &seg.SentimentScore, &seg.Emoji, &seg.PII, &meta); err != nil {
			return c, err
		}
		if meta != nil {
			var m segmentMeta
			if err := json.Unmarshal(meta, &m); err != nil {
				return c, err
			}
			seg.Insight, seg.Assist = m.Insight, m.Assist
		}
		c.Transcript = append(c.Transcript, seg)
	}
	return c, rows.Err()
}

type NewCall struct {
	ID                  string `json:"id"`
	Agent               string `json:"agent"`
	InitiationTimestamp string `json:"initiationTimestamp"` // RFC 3339; empty means now
	CallerPhoneNumber   string `json:"callerPhoneNumber"`
	SystemPhoneNumber   string `json:"systemPhoneNumber"`
}

func (s *Store) CreateCall(ctx context.Context, n NewCall) (Call, error) {
	c, err := scanCall(s.pool.QueryRow(ctx, `
		INSERT INTO calls (id, agent, initiation_timestamp, caller_phone_number, system_phone_number)
		VALUES ($1, $2, COALESCE(NULLIF($3, '')::timestamptz, now()), $4, $5)
		RETURNING `+callColumns,
		n.ID, n.Agent, n.InitiationTimestamp, n.CallerPhoneNumber, n.SystemPhoneNumber))
	if uniqueViolation(err) != "" {
		return Call{}, ErrConflict
	}
	return c, err
}

// CallUpdate holds optional fields; nil leaves the stored value unchanged.
type CallUpdate struct {
	Agent                *string           `json:"agent"`
	Status               *string           `json:"status"`
	Duration             *string           `json:"duration"`
	CallerSentiment      *string           `json:"callerSentiment"`
	CallerSentimentTrend *string           `json:"callerSentimentTrend"`
	AgentSentimentTrend  *string           `json:"agentSentimentTrend"`
	CallerAvgSentiment   *float64          `json:"callerAvgSentiment"`
	AgentAvgSentiment    *float64          `json:"agentAvgSentiment"`
	Categories           *[]string         `json:"categories"`
	AlertCount           *int              `json:"alertCount"`
	Summary              *string           `json:"summary"`
	SentimentHistory     *[]SentimentPoint `json:"sentimentHistory"`
	Analysis             *CallAnalysis     `json:"analysis"`
}

func (s *Store) UpdateCall(ctx context.Context, id string, u CallUpdate) (Call, error) {
	var history *string
	if u.SentimentHistory != nil {
		b, err := json.Marshal(*u.SentimentHistory)
		if err != nil {
			return Call{}, err
		}
		h := string(b)
		history = &h
	}
	var analysis *string
	if u.Analysis != nil {
		b, err := json.Marshal(*u.Analysis)
		if err != nil {
			return Call{}, err
		}
		a := string(b)
		analysis = &a
	}
	return scanCall(s.pool.QueryRow(ctx, `
		UPDATE calls SET
			agent                  = COALESCE($2, agent),
			status                 = COALESCE($3, status),
			duration               = COALESCE($4, duration),
			caller_sentiment       = COALESCE($5, caller_sentiment),
			caller_sentiment_trend = COALESCE($6, caller_sentiment_trend),
			agent_sentiment_trend  = COALESCE($7, agent_sentiment_trend),
			caller_avg_sentiment   = COALESCE($8, caller_avg_sentiment),
			agent_avg_sentiment    = COALESCE($9, agent_avg_sentiment),
			categories             = COALESCE($10, categories),
			alert_count            = COALESCE($11, alert_count),
			summary                = COALESCE($12, summary),
			sentiment_history      = COALESCE($13::jsonb, sentiment_history),
			analysis               = COALESCE($14::jsonb, analysis),
			updated_at             = now()
		WHERE id = $1
		RETURNING `+callColumns,
		id, u.Agent, u.Status, u.Duration, u.CallerSentiment, u.CallerSentimentTrend, u.AgentSentimentTrend,
		u.CallerAvgSentiment, u.AgentAvgSentiment, u.Categories, u.AlertCount, u.Summary, history, analysis))
}

// SetSegmentSentiment records post-call sentiment on one transcript line.
func (s *Store) SetSegmentSentiment(ctx context.Context, callID string, seq int, label string, score float64) error {
	return s.execOne(ctx, `UPDATE transcript_segments SET sentiment = $3, sentiment_score = $4 WHERE call_id = $1 AND seq = $2`,
		callID, seq, label, score)
}

// AppendSegment stores the next transcript segment and returns it with its sequence
// number, plus the call's agent so events can be routed.
func (s *Store) AppendSegment(ctx context.Context, callID string, seg Segment) (Segment, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return seg, "", err
	}
	defer tx.Rollback(ctx)

	// Lock the call row so concurrent appends get distinct sequence numbers
	var agent string
	err = tx.QueryRow(ctx, `UPDATE calls SET updated_at = now() WHERE id = $1 RETURNING agent`, callID).Scan(&agent)
	if errors.Is(err, pgx.ErrNoRows) {
		return seg, "", ErrNotFound
	}
	if err != nil {
		return seg, "", err
	}

	if seg.PII == nil {
		seg.PII = []string{}
	}
	meta, err := metaJSON(seg)
	if err != nil {
		return seg, "", err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO transcript_segments
			(call_id, seq, speaker, time_start, time_end, text, sentiment, sentiment_score, emoji, pii, meta)
		SELECT $1, COALESCE(MAX(seq), 0) + 1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb
		FROM transcript_segments WHERE call_id = $1
		RETURNING seq`,
		callID, seg.Speaker, seg.TimeStart, seg.TimeEnd, seg.Text, seg.Sentiment, seg.SentimentScore, seg.Emoji, seg.PII, meta,
	).Scan(&seg.ID)
	if err != nil {
		return seg, "", err
	}
	return seg, agent, tx.Commit(ctx)
}

// UpdateSegment replaces a stored line's text and its insight and assistant
// details, e.g. when the model has finished writing a suggestion.
func (s *Store) UpdateSegment(ctx context.Context, callID string, seg Segment) error {
	meta, err := metaJSON(seg)
	if err != nil {
		return err
	}
	return s.execOne(ctx, `UPDATE transcript_segments SET text = $3, meta = $4::jsonb WHERE call_id = $1 AND seq = $2`,
		callID, seg.ID, seg.Text, meta)
}
