// Package session stores login sessions in Redis, or in memory when Redis is
// not configured. The browser holds a random token; the store holds only its
// SHA-256 hash, so a dump can't be replayed.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrNoSession = errors.New("no session")

type Session struct {
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expiresAt"` // Unix milliseconds, matching JavaScript's Date.now()
}

type Store struct {
	rdb *redis.Client // nil means in-memory

	mu  sync.Mutex
	mem map[string]Session
}

// NewStore uses Redis when rdb is non-nil, else a process-local map (sessions
// are lost on restart).
func NewStore(rdb *redis.Client) *Store {
	return &Store{rdb: rdb, mem: map[string]Session{}}
}

func key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "lca:session:" + hex.EncodeToString(sum[:])
}

func (s *Store) Create(ctx context.Context, email string, ttl time.Duration) (string, Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", Session{}, err
	}
	token := hex.EncodeToString(b)
	sess := Session{Email: email, ExpiresAt: time.Now().Add(ttl).UnixMilli()}
	if s.rdb == nil {
		s.mu.Lock()
		s.mem[key(token)] = sess
		s.mu.Unlock()
		return token, sess, nil
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return "", Session{}, err
	}
	if err := s.rdb.Set(ctx, key(token), data, ttl).Err(); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

func (s *Store) Get(ctx context.Context, token string) (Session, error) {
	var sess Session
	if s.rdb == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		sess, ok := s.mem[key(token)]
		if !ok {
			return sess, ErrNoSession
		}
		if time.Now().UnixMilli() >= sess.ExpiresAt {
			delete(s.mem, key(token))
			return Session{}, ErrNoSession
		}
		return sess, nil
	}
	data, err := s.rdb.Get(ctx, key(token)).Bytes()
	if errors.Is(err, redis.Nil) {
		return sess, ErrNoSession
	}
	if err != nil {
		return sess, err
	}
	return sess, json.Unmarshal(data, &sess)
}

func (s *Store) Delete(ctx context.Context, token string) error {
	if s.rdb == nil {
		s.mu.Lock()
		delete(s.mem, key(token))
		s.mu.Unlock()
		return nil
	}
	return s.rdb.Del(ctx, key(token)).Err()
}
