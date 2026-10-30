package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type User struct {
	Email        string  `json:"email"`
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	AgentName    *string `json:"agentName"`
	Active       bool    `json:"active"`
	CreatedAt    string  `json:"createdAt"`
	LastSignIn   *string `json:"lastSignIn"`
	PasswordHash string  `json:"-"`
}

var userColumns = `email, name, role, agent_name, active, ` + ts("created_at") + `, ` + ts("last_sign_in") + `, password_hash`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.Email, &u.Name, &u.Role, &u.AgentName, &u.Active, &u.CreatedAt, &u.LastSignIn, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) GetUser(ctx context.Context, email string) (User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, u User) (User, error) {
	created, err := scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, role, agent_name, password_hash)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+userColumns,
		u.Email, u.Name, u.Role, u.AgentName, u.PasswordHash))
	switch uniqueViolation(err) {
	case "":
		return created, err
	case "users_agent_name_key":
		return User{}, ErrAgentNameTaken
	default:
		return User{}, ErrConflict
	}
}

func (s *Store) SetUserActive(ctx context.Context, email string, active bool) error {
	return s.execOne(ctx, `UPDATE users SET active = $2 WHERE email = $1`, email, active)
}

func (s *Store) SetPasswordHash(ctx context.Context, email, hash string) error {
	return s.execOne(ctx, `UPDATE users SET password_hash = $2 WHERE email = $1`, email, hash)
}

func (s *Store) TouchSignIn(ctx context.Context, email string) error {
	return s.execOne(ctx, `UPDATE users SET last_sign_in = now() WHERE email = $1`, email)
}

func (s *Store) DeleteUser(ctx context.Context, email string) error {
	return s.execOne(ctx, `DELETE FROM users WHERE email = $1`, email)
}

func (s *Store) execOne(ctx context.Context, sql string, args ...any) error {
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
