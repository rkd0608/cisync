package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"
)

// AuthUser is the storage projection of ctrl.users. PasswordHash is an
// opaque argon2id encoded string (authusers owns the format); this layer
// never inspects it.
type AuthUser struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

// ErrEmailTaken is the unique-violation mapping for duplicate signups;
// handlers turn it into HTTP 409.
var ErrEmailTaken = errors.New("store: email already registered")

// ErrUserNotFound means no row for that email; handlers MUST map it to the
// same response as a bad password (uniform invalid_credentials, no
// enumeration oracle).
var ErrUserNotFound = errors.New("store: user not found")

const authUserColumns = "id, tenant_id, email, password_hash, created_at, last_login_at"

func scanAuthUser(row pgx.Row) (*AuthUser, error) {
	var u AuthUser
	var email string // citext scans as text
	err := row.Scan(&u.ID, new(string), &email, &u.PasswordHash, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, err
	}
	u.Email = email
	return &u, nil
}

// CreateUser inserts a signup with an app-minted ULID id (migration 0014
// header explains why generation lives here, not in PG).
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (*AuthUser, error) {
	u := &AuthUser{ID: ulid.Make().String(), Email: email, PasswordHash: passwordHash}
	row := s.Pool.QueryRow(ctx,
		`INSERT INTO ctrl.users (id, email, password_hash) VALUES ($1,$2,$3)
		 RETURNING `+authUserColumns,
		u.ID, email, passwordHash)
	inserted, err := scanAuthUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return inserted, nil
}

// FindUserByEmail resolves a login identity; not-found maps to the sentinel
// so callers collapse it into the uniform credential failure.
func (s *Store) FindUserByEmail(ctx context.Context, email string) (*AuthUser, error) {
	u, err := scanAuthUser(s.Pool.QueryRow(ctx,
		`SELECT `+authUserColumns+` FROM ctrl.users WHERE email = $1`, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}

// TouchLogin stamps last_login_at; failures are non-fatal by contract — the
// token was already minted and the response must not fail after the fact.
func (s *Store) TouchLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE ctrl.users SET last_login_at = $2 WHERE id = $1`, userID, at)
	if err != nil {
		err = fmt.Errorf("touch login: %w", err)
	}
	return err
}
