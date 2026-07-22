package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ohchanwu/jobcron/internal/auth"
)

const (
	managedLocalOwnerEmail        = "local-owner@jobcron.example.invalid"
	managedLocalOwnerPasswordHash = "$jobcron$local-login-disabled"
)

// ErrEmailAlreadyExists reports a canonical email uniqueness conflict.
var ErrEmailAlreadyExists = errors.New("storage: email already exists")

// User is an application account row.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateUser creates an application account with a canonical email address.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	user, err := s.insertUser(ctx, s.db, email, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("storage: create user: %w", err)
	}
	return user, nil
}

// CreateUserWithSession atomically creates an account and its initial session.
func (s *Store) CreateUserWithSession(
	ctx context.Context,
	email, passwordHash, tokenHash string,
	expiresAt time.Time,
) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("storage: begin user signup: %w", err)
	}
	defer tx.Rollback()

	user, err := s.insertUser(ctx, tx, email, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("storage: create signup user: %w", err)
	}
	if err := s.insertSession(ctx, tx, user.ID, tokenHash, expiresAt); err != nil {
		return User{}, fmt.Errorf("storage: create signup session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("storage: commit user signup: %w", err)
	}
	return user, nil
}

func (s *Store) insertUser(ctx context.Context, db queryExecer, email, passwordHash string) (User, error) {
	email = auth.NormalizeEmail(email)
	if email == "" {
		return User{}, errors.New("storage: user email is required")
	}
	if passwordHash == "" {
		return User{}, errors.New("storage: user password hash is required")
	}
	now := time.Now().UTC()
	row := db.QueryRowContext(ctx, s.query(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?)
RETURNING id, email, password_hash, created_at, updated_at`), email, passwordHash, now, now)
	user, err := scanUser(row)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" ||
		s.dialect == DialectSQLite && err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
		return User{}, ErrEmailAlreadyExists
	}
	return user, err
}

// CreateOwnerUser creates the first and only owner account for the production
// app. It fails once any user already exists.
func (s *Store) CreateOwnerUser(ctx context.Context, email, passwordHash string) (User, error) {
	email = auth.NormalizeEmail(email)
	if email == "" {
		return User{}, errors.New("storage: owner email is required")
	}
	if passwordHash == "" {
		return User{}, errors.New("storage: owner password hash is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return User{}, fmt.Errorf("storage: begin owner creation: %w", err)
	}
	defer tx.Rollback()

	if err := s.lockUsersForOwnerChange(ctx, tx); err != nil {
		return User{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("storage: count owner users: %w", err)
	}
	if count > 0 {
		return User{}, errors.New("storage: owner user already exists")
	}

	user, err := s.insertUser(ctx, tx, email, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("storage: create owner user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("storage: commit owner creation: %w", err)
	}
	return user, nil
}

// ResetOwnerPassword replaces the password hash for the sole owner account.
func (s *Store) ResetOwnerPassword(ctx context.Context, email, passwordHash string) (User, error) {
	email = auth.NormalizeEmail(email)
	if email == "" {
		return User{}, errors.New("storage: owner email is required")
	}
	if passwordHash == "" {
		return User{}, errors.New("storage: owner password hash is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return User{}, fmt.Errorf("storage: begin owner password reset: %w", err)
	}
	defer tx.Rollback()

	if err := s.lockUsersForOwnerChange(ctx, tx); err != nil {
		return User{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("storage: count owner users: %w", err)
	}
	if count == 0 {
		return User{}, errors.New("storage: owner user does not exist")
	}
	if count > 1 {
		return User{}, errors.New("storage: multiple users exist; refusing owner password reset")
	}

	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx, s.query(`
UPDATE users
   SET password_hash = ?,
       updated_at = ?
 WHERE email = ?
 RETURNING id, email, password_hash, created_at, updated_at`), passwordHash, now, email)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errors.New("storage: owner user does not match email")
	}
	if err != nil {
		return User{}, fmt.Errorf("storage: reset owner password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("storage: commit owner password reset: %w", err)
	}
	return user, nil
}

// ResetUserPassword replaces one user's password hash and revokes all of that
// user's sessions in the same transaction.
func (s *Store) ResetUserPassword(ctx context.Context, email, passwordHash string) (User, error) {
	email = auth.NormalizeEmail(email)
	if email == "" {
		return User{}, errors.New("storage: user email is required")
	}
	if passwordHash == "" {
		return User{}, errors.New("storage: user password hash is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("storage: begin user password reset: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, s.query(`
UPDATE users
   SET password_hash = ?,
       updated_at = ?
 WHERE email = ?
 RETURNING id, email, password_hash, created_at, updated_at`), passwordHash, time.Now().UTC(), email)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errors.New("storage: user does not exist")
	}
	if err != nil {
		return User{}, fmt.Errorf("storage: reset user password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.query(`DELETE FROM sessions WHERE user_id = ?`), user.ID); err != nil {
		return User{}, fmt.Errorf("storage: revoke user sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("storage: commit user password reset: %w", err)
	}
	return user, nil
}

// DeleteUser deletes one exact account. PostgreSQL cascades its private state.
func (s *Store) DeleteUser(ctx context.Context, userID int64) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("storage: begin user deletion: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, s.query(`DELETE FROM users WHERE id = ?`), userID)
	if err != nil {
		return false, fmt.Errorf("storage: delete user: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: count deleted users: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: commit user deletion: %w", err)
	}
	return deleted > 0, nil
}

// DeleteSelf deletes one account only while its expected password hash and the
// caller's current unexpired hashed session still match in the same transaction.
func (s *Store) DeleteSelf(
	ctx context.Context,
	userID int64,
	expectedPasswordHash, sessionHash string,
) (bool, error) {
	if userID <= 0 {
		return false, errors.New("storage: user id is required")
	}
	if expectedPasswordHash == "" {
		return false, errors.New("storage: expected user password hash is required")
	}
	if sessionHash == "" {
		return false, errors.New("storage: current session token hash is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("storage: begin self deletion: %w", err)
	}
	defer tx.Rollback()

	var query string
	var args []any
	if s.dialect == DialectPostgres {
		matched, _, err := s.lockPostgresAccountMutation(ctx, tx, userID, expectedPasswordHash, sessionHash)
		if err != nil {
			return false, fmt.Errorf("storage: lock self deletion: %w", err)
		}
		if !matched {
			return false, nil
		}
		query = `DELETE FROM users WHERE id = ? AND password_hash = ?`
		args = []any{userID, expectedPasswordHash}
	} else {
		now := time.Now().UTC()
		query = `
DELETE FROM users
 WHERE id = ?
   AND password_hash = ?
   AND EXISTS (
       SELECT 1
         FROM sessions
        WHERE sessions.user_id = users.id
          AND session_token_hash = ?
          AND expires_at > ?
   )`
		args = []any{userID, expectedPasswordHash, sessionHash, now}
	}
	result, err := tx.ExecContext(ctx, s.query(query), args...)
	if err != nil {
		return false, fmt.Errorf("storage: delete self: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: count self deletion: %w", err)
	}
	if deleted == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: commit self deletion: %w", err)
	}
	return true, nil
}

func (s *Store) lockPostgresAccountMutation(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	expectedPasswordHash, sessionHash string,
) (bool, time.Time, error) {
	var matched int
	if err := tx.QueryRowContext(ctx, `
SELECT 1
  FROM users
 WHERE id = $1
   AND password_hash = $2
   FOR UPDATE`, userID, expectedPasswordHash).Scan(&matched); errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	} else if err != nil {
		return false, time.Time{}, err
	}
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, `
SELECT expires_at
  FROM sessions
 WHERE user_id = $1
   AND session_token_hash = $2
   FOR UPDATE`, userID, sessionHash).Scan(&expiresAt); errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	} else if err != nil {
		return false, time.Time{}, err
	}
	now := time.Now().UTC()
	return expiresAt.After(now), now, nil
}

func (s *Store) lockUsersForOwnerChange(ctx context.Context, tx *sql.Tx) error {
	if s.dialect == DialectPostgres {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
			return fmt.Errorf("storage: lock users table: %w", err)
		}
	}
	return nil
}

// UserByEmail returns an application user by email address.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, bool, error) {
	email = auth.NormalizeEmail(email)
	if email == "" {
		return User{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, s.query(`
SELECT id, email, password_hash, created_at, updated_at
  FROM users
 WHERE email = ?`), email)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("storage: user by email: %w", err)
	}
	return user, true, nil
}

// UserByID returns an application user by exact positive ID.
func (s *Store) UserByID(ctx context.Context, userID int64) (User, bool, error) {
	if userID <= 0 {
		return User{}, false, nil
	}
	row := s.db.QueryRowContext(ctx, s.query(`
SELECT id, email, password_hash, created_at, updated_at
  FROM users
 WHERE id = ?`), userID)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("storage: user by id: %w", err)
	}
	return user, true, nil
}

// UserIDs returns every positive application user ID in ascending order.
func (s *Store) UserIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users WHERE id > 0 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("storage: list user ids: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: scan user id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list user ids: %w", err)
	}
	return ids, nil
}

// SoleOwnerUserID returns the only application's user ID. It refuses to guess
// when more than one user exists so startup and scheduled work can never spend
// against an arbitrary account.
func (s *Store) SoleOwnerUserID(ctx context.Context) (int64, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM users ORDER BY id LIMIT 2`)
	if err != nil {
		return 0, false, fmt.Errorf("storage: list owner users: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, false, fmt.Errorf("storage: scan owner user: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("storage: list owner users: %w", err)
	}
	switch len(ids) {
	case 0:
		return 0, false, nil
	case 1:
		if ids[0] <= 0 {
			return 0, false, errors.New("storage: sole owner must have a positive user ID")
		}
		return ids[0], true, nil
	default:
		return 0, false, errors.New("storage: multiple users exist; refusing sole-owner operation")
	}
}

// EnsureManagedLocalOwner resolves the fixed no-login owner for the canonical
// managed local PostgreSQL database, creating it only when the users table is
// empty. A sole existing positive user is reused; ambiguous databases are
// refused.
func (s *Store) EnsureManagedLocalOwner(ctx context.Context) (int64, error) {
	if s.dialect != DialectPostgres {
		return 0, errors.New("storage: managed local owner requires PostgreSQL")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, fmt.Errorf("storage: begin managed local owner resolution: %w", err)
	}
	defer tx.Rollback()
	if err := s.lockUsersForOwnerChange(ctx, tx); err != nil {
		return 0, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, email, password_hash FROM users ORDER BY id LIMIT 2`)
	if err != nil {
		return 0, fmt.Errorf("storage: list managed local owners: %w", err)
	}
	type ownerIdentity struct {
		id           int64
		email        string
		passwordHash string
	}
	var owners []ownerIdentity
	for rows.Next() {
		var owner ownerIdentity
		if err := rows.Scan(&owner.id, &owner.email, &owner.passwordHash); err != nil {
			rows.Close()
			return 0, fmt.Errorf("storage: scan managed local owner: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("storage: list managed local owners: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("storage: close managed local owner rows: %w", err)
	}

	var userID int64
	switch len(owners) {
	case 0:
		now := time.Now().UTC()
		if err := tx.QueryRowContext(ctx, s.query(`
INSERT INTO users (email, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?)
RETURNING id`), managedLocalOwnerEmail, managedLocalOwnerPasswordHash, now, now).Scan(&userID); err != nil {
			return 0, fmt.Errorf("storage: create managed local owner: %w", err)
		}
	case 1:
		userID = owners[0].id
	default:
		return 0, errors.New("storage: multiple users exist; refusing managed local owner resolution")
	}
	if userID <= 0 {
		return 0, errors.New("storage: managed local owner must have a positive user ID")
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: commit managed local owner resolution: %w", err)
	}
	return userID, nil
}

// firstUserID is a transitional compatibility helper for legacy no-user
// storage wrappers. Production request paths should pass the authenticated
// user id and avoid this fallback.
func (s *Store) firstUserID(ctx context.Context) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("storage: first user id: %w", err)
	}
	return id, true, nil
}

func scanUser(row rowScanner) (User, error) {
	var user User
	if err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt); err != nil {
		return User{}, err
	}
	return user, nil
}
