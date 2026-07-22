package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ohchanwu/jobcron/internal/auth"
)

// CreateSession stores a hashed session token for a user. Callers must pass the
// SHA-256 token hash, never the raw bearer token.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	if err := s.insertSession(ctx, s.db, userID, tokenHash, expiresAt); err != nil {
		return fmt.Errorf("storage: create session: %w", err)
	}
	return nil
}

func (s *Store) insertSession(ctx context.Context, db queryExecer, userID int64, tokenHash string, expiresAt time.Time) error {
	if userID == 0 {
		return errors.New("storage: session user id is required")
	}
	if tokenHash == "" {
		return errors.New("storage: session token hash is required")
	}
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, s.query(`
INSERT INTO sessions (user_id, session_token_hash, created_at, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)`), userID, tokenHash, now, expiresAt.UTC(), now)
	return err
}

// CreateSessionIfPasswordHash stores a session only while the user's password
// hash still equals the hash the caller verified. PostgreSQL locks the user row
// so a concurrent password mutation either revokes this session afterward or
// changes the hash before this insert can proceed.
func (s *Store) CreateSessionIfPasswordHash(
	ctx context.Context,
	userID int64,
	passwordHash, tokenHash string,
	expiresAt time.Time,
) (bool, error) {
	if userID <= 0 {
		return false, errors.New("storage: session user id is required")
	}
	if passwordHash == "" {
		return false, errors.New("storage: verified password hash is required")
	}
	if tokenHash == "" {
		return false, errors.New("storage: session token hash is required")
	}
	now := time.Now().UTC()
	query := `
INSERT INTO sessions (user_id, session_token_hash, created_at, expires_at, last_seen_at)
SELECT id, ?, ?, ?, ?
  FROM users
 WHERE id = ?
   AND password_hash = ?`
	args := []any{tokenHash, now, expiresAt.UTC(), now, userID, passwordHash}
	if s.dialect == DialectPostgres {
		query = `
WITH authenticated_user AS (
	SELECT id
	  FROM users
	 WHERE id = ?
	   AND password_hash = ?
	 FOR UPDATE
)
INSERT INTO sessions (user_id, session_token_hash, created_at, expires_at, last_seen_at)
SELECT id, ?, ?, ?, ?
  FROM authenticated_user`
		args = []any{userID, passwordHash, tokenHash, now, expiresAt.UTC(), now}
	}
	result, err := s.db.ExecContext(ctx, s.query(query), args...)
	if err != nil {
		return false, fmt.Errorf("storage: create verified session: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: count verified sessions: %w", err)
	}
	return created == 1, nil
}

// ChangePassword replaces one user's expected password hash only while the
// caller's current hashed session still belongs to that user and is unexpired.
// It revokes every other session in the same transaction.
func (s *Store) ChangePassword(
	ctx context.Context,
	userID int64,
	expectedPasswordHash, passwordHash, keepSessionHash string,
) (bool, error) {
	if userID <= 0 {
		return false, errors.New("storage: user id is required")
	}
	if expectedPasswordHash == "" {
		return false, errors.New("storage: expected user password hash is required")
	}
	if passwordHash == "" {
		return false, errors.New("storage: user password hash is required")
	}
	if keepSessionHash == "" {
		return false, errors.New("storage: current session token hash is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("storage: begin password change: %w", err)
	}
	defer tx.Rollback()

	var query string
	var args []any
	if s.dialect == DialectPostgres {
		matched, mutationTime, err := s.lockPostgresAccountMutation(ctx, tx, userID, expectedPasswordHash, keepSessionHash)
		if err != nil {
			return false, fmt.Errorf("storage: lock password change: %w", err)
		}
		if !matched {
			return false, nil
		}
		query = `
UPDATE users
   SET password_hash = ?,
       updated_at = ?
 WHERE id = ?
   AND password_hash = ?`
		args = []any{passwordHash, mutationTime, userID, expectedPasswordHash}
	} else {
		now := time.Now().UTC()
		query = `
UPDATE users
   SET password_hash = ?,
       updated_at = ?
 WHERE id = ?
   AND password_hash = ?
   AND EXISTS (
       SELECT 1
         FROM sessions
        WHERE sessions.user_id = users.id
          AND session_token_hash = ?
          AND expires_at > ?
   )`
		args = []any{passwordHash, now, userID, expectedPasswordHash, keepSessionHash, now}
	}
	result, err := tx.ExecContext(ctx, s.query(query), args...)
	if err != nil {
		return false, fmt.Errorf("storage: change password: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: count changed users: %w", err)
	}
	if updated == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, s.query(`
DELETE FROM sessions
 WHERE user_id = ?
   AND session_token_hash <> ?`), userID, keepSessionHash); err != nil {
		return false, fmt.Errorf("storage: revoke other sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: commit password change: %w", err)
	}
	return true, nil
}

// UserBySessionToken returns the user for an unexpired raw session token.
func (s *Store) UserBySessionToken(ctx context.Context, token string) (User, bool, error) {
	if token == "" {
		return User{}, false, nil
	}
	tokenHash := auth.HashSessionToken(token)
	now := time.Now().UTC()
	row := s.db.QueryRowContext(ctx, s.query(`
SELECT u.id, u.email, u.password_hash, u.created_at, u.updated_at
  FROM sessions s
  JOIN users u ON u.id = s.user_id
 WHERE s.session_token_hash = ?
   AND s.expires_at > ?`), tokenHash, now)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("storage: user by session token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, s.query(`
UPDATE sessions
   SET last_seen_at = ?
 WHERE session_token_hash = ?`), now, tokenHash); err != nil {
		return User{}, false, fmt.Errorf("storage: update session last seen: %w", err)
	}
	return user, true, nil
}

// RevokeSessionToken deletes the session row for a raw bearer token. Missing
// rows are treated as already-revoked success.
func (s *Store) RevokeSessionToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, s.query(`
DELETE FROM sessions
 WHERE session_token_hash = ?`), auth.HashSessionToken(token)); err != nil {
		return fmt.Errorf("storage: revoke session token: %w", err)
	}
	return nil
}
