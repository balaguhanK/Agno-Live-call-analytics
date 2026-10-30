package api

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"lca-backend/internal/store"
)

// A request for information is never listed as an issue, even when the model names it
func TestCallIssuesLeavesOutInquiries(t *testing.T) {
	tr := []store.Segment{
		{ID: 1, Speaker: "AGENT", Text: "Calling Acme Bank. How can I help you?"},
		{ID: 2, Speaker: "CUSTOMER", Text: "I have lost my credit card."},
		{ID: 3, Speaker: "AGENT", Text: "I'm sorry to hear that. I'll block it now."},
		{ID: 4, Speaker: "CUSTOMER", Text: "Also I want to know the personal loan."},
	}
	model := []struct {
		Name     string `json:"name"`
		Evidence string `json:"evidence"`
	}{
		{"Lost credit card", "I have lost my credit card"},
		{"Personal loan inquiry", "I want to know the personal loan"},
		{"Personal loan", "I want to know the personal loan"},
	}
	got := callIssues(tr, []string{"CardServices", "PersonalLoan"}, model, "lost credit card", "card services", nil)
	if !slices.Equal(got, []string{"Lost credit card"}) {
		t.Errorf("issues = %q, want [Lost credit card]", got)
	}
}

// A call saved under older rules, with requests for information flagged as
// issues, shows what a new call would once the server starts
func TestOldCallInquiriesAreNotIssues(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer(t, func(o *Options) {
		st := o.Store
		if _, err := st.CreateCall(ctx, store.NewCall{ID: "old2", Agent: "BobS", CallerPhoneNumber: "+15550011",
			InitiationTimestamp: time.Now().UTC().Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
		for _, l := range [][2]string{
			{"CUSTOMER", "I also want to know about the question... About the question."},
			{"CUSTOMER", "I want to know about the person wrong."},
			{"CUSTOMER", "I have lost my credit card."},
		} {
			if _, _, err := st.AppendSegment(ctx, "old2", store.Segment{Speaker: l[0], Text: l[1], Insight: store.InsightIssue}); err != nil {
				t.Fatal(err)
			}
		}
		done := "Done"
		old := store.CallAnalysis{Issues: []string{"Personal loan inquiry", "Lost credit card"}, Model: "gemma2:2b"}
		if _, err := st.UpdateCall(ctx, "old2", store.CallUpdate{Status: &done, Analysis: &old}); err != nil {
			t.Fatal(err)
		}
		o.Recategorize = true
	})
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	var got store.Call
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got, _ = postJSONGetCall(t, agent, srv.URL+"/api/calls/old2"); got.Analysis != nil && slices.Equal(got.Analysis.Issues, []string{"Lost credit card"}) {
			break
		}
	}
	if got.Analysis == nil || !slices.Equal(got.Analysis.Issues, []string{"Lost credit card"}) {
		t.Fatalf("issues = %+v", got.Analysis)
	}
	flagged := 0
	for _, s := range got.Transcript {
		if s.Insight == store.InsightIssue {
			flagged++
			if !strings.Contains(s.Text, "lost") {
				t.Errorf("a request for information is still flagged: %q", s.Text)
			}
		}
	}
	if flagged != 1 {
		t.Errorf("%d lines flagged as issues, want 1", flagged)
	}
}
