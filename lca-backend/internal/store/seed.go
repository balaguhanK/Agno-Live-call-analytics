package store

import (
	"context"
	"log/slog"
)

type SeedUser struct {
	Email, Name, Role, Password string
	AgentName                   *string
}

func ptr(s string) *string { return &s }

// DemoUsers match the demo accounts shown on the dashboard login page.
var DemoUsers = []SeedUser{
	{Email: "admin@cxplatform.com", Name: "Admin", Role: "admin", Password: "admin123"},
	{Email: "agent@cxplatform.com", Name: "Bob Smith", Role: "agent", AgentName: ptr("BobS"), Password: "agent123"},
	{Email: "babu@cxplatform.com", Name: "Babu S", Role: "agent", AgentName: ptr("BabuS"), Password: "agent123"},
}

// seedTarget is what seeding needs; both Store and Memory satisfy it.
type seedTarget interface {
	CountUsers(context.Context) (int, error)
	CreateUser(context.Context, User) (User, error)
}

// SeedUsers creates the demo accounts when the users table is empty.
func (s *Store) SeedUsers(ctx context.Context, hash func(string) (string, error)) error {
	return seedUsers(ctx, s, hash)
}

func seedUsers(ctx context.Context, s seedTarget, hash func(string) (string, error)) error {
	n, err := s.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	for _, su := range DemoUsers {
		h, err := hash(su.Password)
		if err != nil {
			return err
		}
		u := User{Email: su.Email, Name: su.Name, Role: su.Role, AgentName: su.AgentName, PasswordHash: h}
		if _, err := s.CreateUser(ctx, u); err != nil {
			return err
		}
	}
	slog.Warn("seeded demo users with default passwords; change them before going live", "count", len(DemoUsers))
	return nil
}
