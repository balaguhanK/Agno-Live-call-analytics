package events

import (
	"context"
	"testing"
)

func TestInProcessPublishFiltersByAgent(t *testing.T) {
	h := NewHub(nil)
	admin := h.Register("")
	mine := h.Register("alice")
	other := h.Register("bob")

	if err := h.Publish(context.Background(), Event{Type: "call", CallID: "c1", Agent: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(admin.Send) != 1 || len(mine.Send) != 1 {
		t.Fatalf("admin got %d, alice got %d; want 1 each", len(admin.Send), len(mine.Send))
	}
	if len(other.Send) != 0 {
		t.Fatalf("bob got %d events, want 0", len(other.Send))
	}
}
