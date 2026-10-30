package store

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
)

const tsLayout = "2006-01-02T15:04:05.000Z"

func nowTS() string { return time.Now().UTC().Format(tsLayout) }

// Memory is a process-local store for running without Postgres. Data is lost
// on restart. Methods mirror Store and return copies, never shared slices.
type Memory struct {
	mu       sync.Mutex
	users    map[string]User
	userSeq  map[string]int // creation order, for users created in the same millisecond
	nextSeq  int
	calls    map[string]Call
	segments map[string][]Segment
}

func NewMemory() *Memory {
	return &Memory{users: map[string]User{}, userSeq: map[string]int{}, calls: map[string]Call{}, segments: map[string][]Segment{}}
}

// Close is a no-op; it lets Memory stand in wherever Store is closed.
func (m *Memory) Close() {}

func (m *Memory) CountUsers(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *Memory) GetUser(_ context.Context, email string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[email]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) ListUsers(context.Context) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	users := make([]User, 0, len(m.users))
	for _, u := range m.users {
		users = append(users, u)
	}
	slices.SortFunc(users, func(a, b User) int {
		// Same order as Postgres: creation time, then creation order
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(m.userSeq[a.Email], m.userSeq[b.Email]))
	})
	return users, nil
}

func (m *Memory) CreateUser(_ context.Context, u User) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.Email]; ok {
		return User{}, ErrConflict
	}
	if u.AgentName != nil {
		for _, other := range m.users {
			if other.AgentName != nil && *other.AgentName == *u.AgentName {
				return User{}, ErrAgentNameTaken
			}
		}
	}
	u.Active = true
	u.CreatedAt = nowTS()
	u.LastSignIn = nil
	m.users[u.Email] = u
	m.nextSeq++
	m.userSeq[u.Email] = m.nextSeq
	return u, nil
}

func (m *Memory) updateUser(email string, f func(*User)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[email]
	if !ok {
		return ErrNotFound
	}
	f(&u)
	m.users[email] = u
	return nil
}

func (m *Memory) SetUserActive(_ context.Context, email string, active bool) error {
	return m.updateUser(email, func(u *User) { u.Active = active })
}

func (m *Memory) SetPasswordHash(_ context.Context, email, hash string) error {
	return m.updateUser(email, func(u *User) { u.PasswordHash = hash })
}

func (m *Memory) TouchSignIn(_ context.Context, email string) error {
	return m.updateUser(email, func(u *User) { now := nowTS(); u.LastSignIn = &now })
}

func (m *Memory) DeleteUser(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[email]; !ok {
		return ErrNotFound
	}
	delete(m.users, email)
	delete(m.userSeq, email)
	return nil
}

func copyCall(c Call) Call {
	c.Categories = slices.Clone(c.Categories)
	c.SentimentHistory = slices.Clone(c.SentimentHistory)
	c.Transcript = nil
	if c.Summary != nil {
		s := *c.Summary
		c.Summary = &s
	}
	if c.Analysis != nil {
		a := copyAnalysis(*c.Analysis)
		c.Analysis = &a
	}
	return c
}

// compareCalls orders by the given sort key, then by ID so pages are stable.
func compareCalls(key string, a, b Call) int {
	var c int
	switch key {
	case "id":
	case "agent":
		c = cmp.Compare(a.Agent, b.Agent)
	case "callerPhoneNumber":
		c = cmp.Compare(a.CallerPhoneNumber, b.CallerPhoneNumber)
	case "status":
		c = cmp.Compare(a.Status, b.Status)
	case "callerSentiment":
		c = cmp.Compare(a.CallerAvgSentiment, b.CallerAvgSentiment)
	case "callerSentimentTrend":
		c = cmp.Compare(a.CallerSentimentTrend, b.CallerSentimentTrend)
	case "duration":
		c = cmp.Compare(a.Duration, b.Duration)
	case "alertCount":
		c = cmp.Compare(a.AlertCount, b.AlertCount)
	default:
		c = cmp.Compare(a.InitiationTimestamp, b.InitiationTimestamp)
	}
	return cmp.Or(c, cmp.Compare(a.ID, b.ID))
}

// ListCalls returns one page of matching calls, without transcripts, and the
// total number of matches.
func (m *Memory) ListCalls(_ context.Context, q CallQuery) ([]Call, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := []Call{}
	for _, c := range m.calls {
		if q.matches(c) {
			calls = append(calls, copyCall(c))
		}
	}
	key := q.sortKey()
	slices.SortFunc(calls, func(a, b Call) int {
		if q.Desc {
			a, b = b, a
		}
		return compareCalls(key, a, b)
	})
	total := len(calls)
	calls = calls[min(q.Offset, total):]
	if q.Limit > 0 && len(calls) > q.Limit {
		calls = calls[:q.Limit]
	}
	return calls, total, nil
}

func (m *Memory) CallCountsByAgent(context.Context) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string]int{}
	for _, c := range m.calls {
		counts[c.Agent]++
	}
	return counts, nil
}

func (m *Memory) GetCall(_ context.Context, id string) (Call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calls[id]
	if !ok {
		return Call{}, ErrNotFound
	}
	c = copyCall(c)
	c.Transcript = slices.Clone(m.segments[id])
	if c.Transcript == nil {
		c.Transcript = []Segment{}
	}
	return c, nil
}

func (m *Memory) CreateCall(_ context.Context, n NewCall) (Call, error) {
	started := time.Now()
	if n.InitiationTimestamp != "" {
		t, err := time.Parse(time.RFC3339, n.InitiationTimestamp)
		if err != nil {
			return Call{}, err
		}
		started = t
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.calls[n.ID]; ok {
		return Call{}, ErrConflict
	}
	c := Call{
		ID: n.ID, Agent: n.Agent,
		InitiationTimestamp: started.UTC().Format(tsLayout), UpdatedAt: nowTS(),
		CallerPhoneNumber: n.CallerPhoneNumber, SystemPhoneNumber: n.SystemPhoneNumber,
		Status: "In Progress", CallerSentiment: "Neutral",
		CallerSentimentTrend: "Flat", AgentSentimentTrend: "Flat", Duration: "00:00:00",
		Categories: []string{}, SentimentHistory: []SentimentPoint{},
	}
	m.calls[c.ID] = c
	return copyCall(c), nil
}

func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func (m *Memory) UpdateCall(_ context.Context, id string, u CallUpdate) (Call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calls[id]
	if !ok {
		return Call{}, ErrNotFound
	}
	setIf(&c.Agent, u.Agent)
	setIf(&c.Status, u.Status)
	setIf(&c.Duration, u.Duration)
	setIf(&c.CallerSentiment, u.CallerSentiment)
	setIf(&c.CallerSentimentTrend, u.CallerSentimentTrend)
	setIf(&c.AgentSentimentTrend, u.AgentSentimentTrend)
	setIf(&c.CallerAvgSentiment, u.CallerAvgSentiment)
	setIf(&c.AgentAvgSentiment, u.AgentAvgSentiment)
	setIf(&c.AlertCount, u.AlertCount)
	if u.Categories != nil {
		c.Categories = slices.Clone(*u.Categories)
	}
	if u.SentimentHistory != nil {
		c.SentimentHistory = slices.Clone(*u.SentimentHistory)
	}
	if u.Summary != nil {
		c.Summary = &Summary{Text: *u.Summary}
	}
	if u.Analysis != nil {
		a := copyAnalysis(*u.Analysis)
		c.Analysis = &a
	}
	c.UpdatedAt = nowTS()
	m.calls[id] = c
	return copyCall(c), nil
}

func (m *Memory) AppendSegment(_ context.Context, callID string, seg Segment) (Segment, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calls[callID]
	if !ok {
		return seg, "", ErrNotFound
	}
	if seg.PII == nil {
		seg.PII = []string{}
	}
	seg.ID = len(m.segments[callID]) + 1
	seg.Assist = copyAssist(seg.Assist)
	m.segments[callID] = append(m.segments[callID], seg)
	c.UpdatedAt = nowTS()
	m.calls[callID] = c
	return seg, c.Agent, nil
}

// SetSegmentSentiment records post-call sentiment on one transcript line.
func (m *Memory) SetSegmentSentiment(_ context.Context, callID string, seq int, label string, score float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	segs := m.segments[callID]
	if seq < 1 || seq > len(segs) {
		return ErrNotFound
	}
	segs[seq-1].Sentiment, segs[seq-1].SentimentScore = &label, &score
	return nil
}

func (m *Memory) SeedUsers(ctx context.Context, hash func(string) (string, error)) error {
	return seedUsers(ctx, m, hash)
}

// copyAnalysis copies the parts of an analysis that would otherwise be shared.
func copyAnalysis(a CallAnalysis) CallAnalysis {
	a.Agent.Strengths = slices.Clone(a.Agent.Strengths)
	a.Agent.Improvements = slices.Clone(a.Agent.Improvements)
	a.Issues = slices.Clone(a.Issues)
	a.Actions = slices.Clone(a.Actions)
	if a.Entities != nil {
		entities := make(map[string][]string, len(a.Entities))
		for k, v := range a.Entities {
			entities[k] = slices.Clone(v)
		}
		a.Entities = entities
	}
	if a.GenAI != nil {
		g := *a.GenAI
		a.GenAI = &g
	}
	quotes := []**Quote{&a.Quotes.CallerLow, &a.Quotes.AgentBest, &a.Quotes.AgentWorst, &a.Query.Evidence}
	for i := range a.Actions {
		quotes = append(quotes, &a.Actions[i].Evidence)
	}
	for _, q := range quotes {
		if *q != nil {
			c := **q
			*q = &c
		}
	}
	return a
}

func copyAssist(a *SegmentAssist) *SegmentAssist {
	if a == nil {
		return nil
	}
	c := *a
	c.Sources = slices.Clone(a.Sources)
	c.Steps = slices.Clone(a.Steps)
	return &c
}

// UpdateSegment replaces a stored line's text and its insight and assistant details.
func (m *Memory) UpdateSegment(_ context.Context, callID string, seg Segment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	segs := m.segments[callID]
	if seg.ID < 1 || seg.ID > len(segs) {
		return ErrNotFound
	}
	cur := &segs[seg.ID-1]
	cur.Text, cur.Insight, cur.Assist = seg.Text, seg.Insight, copyAssist(seg.Assist)
	return nil
}
