package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lca-backend/internal/events"
	"lca-backend/internal/session"
	"lca-backend/internal/store"
)

// newTestServer runs the API over an in-memory store with the demo users and
// four calls: three for BobS (one 50 hours old) and one for BabuS.
func newTestServer(t *testing.T, configure ...func(*Options)) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	mem := store.NewMemory()
	if err := mem.SeedUsers(ctx, HashPassword); err != nil {
		t.Fatal(err)
	}
	add := func(id, agent, phone string, ago time.Duration, cats ...string) {
		_, err := mem.CreateCall(ctx, store.NewCall{ID: id, Agent: agent, CallerPhoneNumber: phone,
			InitiationTimestamp: time.Now().Add(-ago).UTC().Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		if len(cats) > 0 {
			_, _ = mem.UpdateCall(ctx, id, store.CallUpdate{Categories: &cats})
		}
	}
	add("c1", "BobS", "+15550001", 30*time.Minute, "Billing")
	add("c2", "BabuS", "+15550002", time.Hour)
	add("c3", "BobS", "+15550003", 10*time.Minute)
	add("c4", "BobS", "=HYPERLINK(1)", 50*time.Hour)

	opts := Options{
		Store: mem, Sessions: session.NewStore(nil), Hub: events.NewHub(nil),
		IngestToken: "test-ingest-token-0123456789",
		Health:      func(context.Context) error { return nil },
	}
	for _, f := range configure {
		f(&opts)
	}
	srv := httptest.NewServer(New(opts))
	t.Cleanup(srv.Close)
	return srv
}

// signIn returns a client carrying a session cookie for the given demo account.
func signIn(t *testing.T, srv *httptest.Server, email, password, role string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]any{"email": email, "password": password, "role": role})
	res, err := c.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign in %s: %s", email, res.Status)
	}
	return c
}

func listPage(t *testing.T, c *http.Client, url string) (callPage, int) {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var p callPage
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
	}
	return p, res.StatusCode
}

func callIDs(p callPage) string {
	ids := make([]string, len(p.Items))
	for i, c := range p.Items {
		ids[i] = c.ID
	}
	return strings.Join(ids, ",")
}

func TestListCalls(t *testing.T) {
	srv := newTestServer(t)
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")
	base := srv.URL + "/api/calls"

	cases := []struct {
		name   string
		client *http.Client
		query  string
		want   string
		total  int
	}{
		{"admin sees all, newest first", admin, "", "c3,c1,c2,c4", 4},
		{"agent sees only own calls", agent, "", "c3,c1,c4", 3},
		{"agent can't search into other agents' calls", agent, "?q=BabuS", "", 0},
		{"2 hour window", admin, "?hours=2", "c3,c1,c2", 3},
		{"search by category", admin, "?q=billing", "c1", 1},
		{"sort ascending by id", admin, "?sort=id&order=asc", "c1,c2,c3,c4", 4},
		{"second page", admin, "?sort=id&order=asc&pageSize=3&page=2", "c4", 4},
	}
	for _, tc := range cases {
		p, status := listPage(t, tc.client, base+tc.query)
		if status != http.StatusOK || callIDs(p) != tc.want || p.Total != tc.total {
			t.Errorf("%s: status %d, got %q (total %d), want %q (total %d)",
				tc.name, status, callIDs(p), p.Total, tc.want, tc.total)
		}
	}
}

func TestListCallsRejectsBadParams(t *testing.T) {
	srv := newTestServer(t)
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	for _, q := range []string{
		"?sort=password_hash", "?order=sideways", "?hours=-1", "?hours=abc",
		"?page=0", "?pageSize=1000", "?q=" + strings.Repeat("x", maxSearchLen+1),
	} {
		if _, status := listPage(t, admin, srv.URL+"/api/calls"+q); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, status)
		}
	}
}

func TestListCallsNeedsSession(t *testing.T) {
	srv := newTestServer(t)
	if _, status := listPage(t, http.DefaultClient, srv.URL+"/api/calls"); status != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", status)
	}
}

func exportCSV(t *testing.T, c *http.Client, url string) [][]string {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("export: %s %s", res.Status, b)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("content type %q", res.Header.Get("Content-Type"))
	}
	rows, err := csv.NewReader(res.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestExportCalls(t *testing.T) {
	srv := newTestServer(t)
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	agent := signIn(t, srv, "agent@cxplatform.com", "agent123", "agent")

	rows := exportCSV(t, admin, srv.URL+"/api/calls/export?sort=id&order=asc")
	if len(rows) != 5 || rows[0][0] != "Call ID" {
		t.Fatalf("got %d rows, header %v", len(rows), rows[0])
	}
	if rows[1][3] != "+15550001" {
		t.Errorf("phone number was altered: %q", rows[1][3])
	}
	if rows[4][3] != "'=HYPERLINK(1)" {
		t.Errorf("formula not neutralized: %q", rows[4][3])
	}

	rows = exportCSV(t, admin, srv.URL+"/api/calls/export?ids=c2,c3&sort=id&order=asc")
	if len(rows) != 3 || rows[1][0] != "c2" || rows[2][0] != "c3" {
		t.Errorf("selected export rows = %v", rows)
	}

	// Asking for another agent's call by ID must not leak it
	rows = exportCSV(t, agent, srv.URL+"/api/calls/export?ids=c2")
	if len(rows) != 1 {
		t.Errorf("agent exported other agent's call: %v", rows)
	}
}

func TestListUsersIncludesCallCounts(t *testing.T) {
	srv := newTestServer(t)
	admin := signIn(t, srv, "admin@cxplatform.com", "admin123", "admin")
	res, err := admin.Get(srv.URL + "/api/users")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var users []struct {
		Email     string `json:"email"`
		CallCount int    `json:"callCount"`
	}
	if err := json.NewDecoder(res.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, u := range users {
		counts[u.Email] = u.CallCount
	}
	if counts["agent@cxplatform.com"] != 3 || counts["babu@cxplatform.com"] != 1 || counts["admin@cxplatform.com"] != 0 {
		t.Errorf("call counts = %v", counts)
	}
}
