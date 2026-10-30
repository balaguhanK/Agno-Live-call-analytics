package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewStore(nil)

	token, _, err := s.Create(ctx, "agent@example.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Get(ctx, token)
	if err != nil || sess.Email != "agent@example.com" {
		t.Fatalf("Get = %+v, %v", sess, err)
	}
	if err := s.Delete(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after Delete, err = %v, want ErrNoSession", err)
	}
}

func TestMemoryStoreExpires(t *testing.T) {
	ctx := context.Background()
	s := NewStore(nil)

	token, _, err := s.Create(ctx, "agent@example.com", -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired session err = %v, want ErrNoSession", err)
	}
}
