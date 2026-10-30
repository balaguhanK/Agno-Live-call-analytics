package store

// CallAnalysis is the post-call analysis of how the caller felt, what they
// asked for and how the agent spoke. Stats and quotes come straight from the
// transcript; the insights are written by the chat model when there is one.
type CallAnalysis struct {
	Stats  CallStats      `json:"stats"`
	Caller CallerInsight  `json:"caller"`
	Query  QueryInsight   `json:"query"`
	Agent  AgentInsight   `json:"agent"`
	Quotes AnalysisQuotes `json:"quotes"`
	// The caller's issues as short names, e.g. "Order cancellation"
	Issues []string `json:"issues,omitempty"`
	// What the agent did or agreed to do, as in the summary's ACTIONS
	Actions []ActionItem `json:"actions,omitempty"`
	// Named things mentioned on the call, by type (EntityPerson, ...)
	Entities map[string][]string `json:"entities,omitempty"`
	// Worked out when the call is read, for its transcript on the post-call
	// page: the sentence of each issue, action and outcome line, and the line
	// each category's topic first came up on
	Highlights    []Highlight    `json:"highlights,omitempty"`
	CategoryLines map[string]int `json:"categoryLines,omitempty"`
	// Post-call analytics insights; rule-based until the model writes them
	GenAI *GenAIInsights `json:"genAI,omitempty"`
	// Model that wrote the insights; empty when they are rule-based
	Model string `json:"model,omitempty"`
	// The model is still writing; the rule-based insights show until then
	Pending bool `json:"pending,omitempty"`
}

type CallStats struct {
	CallerLines   int `json:"callerLines"`
	AgentLines    int `json:"agentLines"`
	CallerTalkPct int `json:"callerTalkPct"` // share of the words spoken
	AgentTalkPct  int `json:"agentTalkPct"`
	// Caller's mood over the first and last parts of the call: Positive, Neutral or Negative
	CallerStart         string `json:"callerStart"`
	CallerEnd           string `json:"callerEnd"`
	CallerNegativeLines int    `json:"callerNegativeLines"`
	AgentPositiveLines  int    `json:"agentPositiveLines"`
	AgentNegativeLines  int    `json:"agentNegativeLines"`
}

type CallerInsight struct {
	Mood    string `json:"mood"`
	Summary string `json:"summary"`
}

type QueryInsight struct {
	Issue string `json:"issue"`
	// Resolved, Partly resolved, Not resolved, or empty when unknown
	Outcome string `json:"outcome,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// The transcript line that shows the outcome; a model's outcome without one isn't kept
	Evidence *Quote `json:"evidence,omitempty"`
}

type AgentInsight struct {
	Tone         string   `json:"tone"`
	Summary      string   `json:"summary"`
	Strengths    []string `json:"strengths,omitempty"`
	Improvements []string `json:"improvements,omitempty"`
	Rating       int      `json:"rating,omitempty"` // 1 to 5; 0 when not rated
}

// AnalysisQuotes are exact transcript lines that back the analysis up.
type AnalysisQuotes struct {
	CallerLow  *Quote `json:"callerLow,omitempty"`  // the caller's most negative line
	AgentBest  *Quote `json:"agentBest,omitempty"`  // the agent's most positive line
	AgentWorst *Quote `json:"agentWorst,omitempty"` // only when the agent sounded negative
}

type Quote struct {
	Time  string  `json:"time"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
}

// Entity types found in a transcript
const (
	EntityPerson       = "PERSON"
	EntityOrganization = "ORGANIZATION"
	EntityQuantity     = "QUANTITY"
	EntityDate         = "DATE"
	EntityReference    = "REFERENCE" // order, ticket and account numbers
	EntityProduct      = "PRODUCT"   // products and services: "rewards card", "life insurance"
)

// Highlight marks the sentence of a line that is the caller's issue, an
// agent's promise (action) or what the agent did (outcome).
type Highlight struct {
	Line int    `json:"line"` // the segment's ID
	Kind string `json:"kind"` // InsightIssue, InsightAction or InsightOutcome
	Text string `json:"text"` // the sentence, exactly as in the line
}

// ActionItem is one thing the agent did or agreed to do on the call.
type ActionItem struct {
	Title  string `json:"title"` // short, starting with a verb
	Detail string `json:"detail,omitempty"`
	Owner  string `json:"owner,omitempty"`
	Due    string `json:"due,omitempty"`
	// The agent's line the action comes from, exactly as transcribed
	Evidence *Quote `json:"evidence,omitempty"`
}

// GenAIInsights are the post-call analytics insights about the whole call.
type GenAIInsights struct {
	Summary    string `json:"summary"`
	Topic      string `json:"topic"`
	Product    string `json:"product"`
	Politeness string `json:"politeness"` // "yes" or "no": was the agent polite
	Actions    string `json:"actions"`    // what the agent did for the caller
	Model      string `json:"model,omitempty"`
}
