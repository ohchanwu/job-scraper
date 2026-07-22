package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type accountMutationResult struct {
	changed bool
	err     error
}

func TestChangePasswordUpdatesHashAndKeepsOnlyCurrentSession(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	for _, hash := range []string{"old-session", "current-session", "other-session"} {
		if err := st.CreateSession(ctx, user.ID, hash, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession(%q): %v", hash, err)
		}
	}

	changed, err := st.ChangePassword(ctx, user.ID, "old-hash", "new-hash", "current-session")
	if err != nil || !changed {
		t.Fatalf("ChangePassword: changed=%v err=%v", changed, err)
	}
	var passwordHash string
	if err := st.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = $1`, user.ID).Scan(&passwordHash); err != nil {
		t.Fatalf("query password hash: %v", err)
	}
	if passwordHash != "new-hash" {
		t.Fatalf("password hash = %q, want new-hash", passwordHash)
	}
	var sessionHashes []string
	rows, err := st.db.QueryContext(ctx, `SELECT session_token_hash FROM sessions WHERE user_id = $1`, user.ID)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan session: %v", err)
		}
		sessionHashes = append(sessionHashes, hash)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sessions: %v", err)
	}
	if len(sessionHashes) != 1 || sessionHashes[0] != "current-session" {
		t.Fatalf("session hashes = %v, want [current-session]", sessionHashes)
	}
}

func TestChangePasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.CreateSession(ctx, user.ID, "current-session", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.CreateSession(ctx, user.ID, "other-session", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession other: %v", err)
	}
	installRejectingSessionDeleteTrigger(t, st)

	if _, err := st.ChangePassword(ctx, user.ID, "old-hash", "new-hash", "current-session"); err == nil {
		t.Fatal("ChangePassword error = nil, want session revocation failure")
	}
	assertPasswordHash(t, st, user.ID, "old-hash")
}

func TestChangePasswordRequiresExpectedHashAndCurrentSession(t *testing.T) {
	forEachAccountStore(t, func(t *testing.T, st *Store) {
		ctx := context.Background()
		user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		for _, session := range []string{"current-session", "other-session"} {
			if err := st.CreateSession(ctx, user.ID, session, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("CreateSession(%q): %v", session, err)
			}
		}

		changed, err := st.ChangePassword(ctx, user.ID, "stale-hash", "stale-update", "current-session")
		if err != nil || changed {
			t.Fatalf("stale ChangePassword: changed=%v err=%v, want false nil", changed, err)
		}
		assertPasswordHash(t, st, user.ID, "old-hash")
		assertRowCount(t, st, "sessions", "user_id", user.ID, 2)

		if _, err := st.db.ExecContext(ctx, st.query(`DELETE FROM sessions WHERE session_token_hash = ?`), "current-session"); err != nil {
			t.Fatalf("revoke current session: %v", err)
		}
		changed, err = st.ChangePassword(ctx, user.ID, "old-hash", "missing-session-update", "current-session")
		if err != nil || changed {
			t.Fatalf("revoked-session ChangePassword: changed=%v err=%v, want false nil", changed, err)
		}
		assertPasswordHash(t, st, user.ID, "old-hash")
		assertRowCount(t, st, "sessions", "user_id", user.ID, 1)

		if err := st.CreateSession(ctx, user.ID, "current-session", time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("restore current session: %v", err)
		}
		changed, err = st.ChangePassword(ctx, user.ID, "old-hash", "new-hash", "current-session")
		if err != nil || !changed {
			t.Fatalf("valid ChangePassword: changed=%v err=%v, want true nil", changed, err)
		}
		assertPasswordHash(t, st, user.ID, "new-hash")
		assertRowCount(t, st, "sessions", "user_id", user.ID, 1)
	})
}

func TestAccountMutationsRejectExpiredSession(t *testing.T) {
	for _, mutation := range []string{"change password", "delete self"} {
		t.Run(mutation, func(t *testing.T) {
			forEachAccountStore(t, func(t *testing.T, st *Store) {
				ctx := context.Background()
				user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
				if err != nil {
					t.Fatalf("CreateUser: %v", err)
				}
				if err := st.CreateSession(ctx, user.ID, "expired-session", time.Now().Add(-time.Hour)); err != nil {
					t.Fatalf("CreateSession expired: %v", err)
				}
				if err := st.CreateSession(ctx, user.ID, "other-session", time.Now().Add(time.Hour)); err != nil {
					t.Fatalf("CreateSession other: %v", err)
				}
				const profileJSON = `{"career_years":0}`
				if _, _, err := st.SaveProfileForUser(ctx, user.ID, profileJSON); err != nil {
					t.Fatalf("SaveProfileForUser: %v", err)
				}

				var changed bool
				if mutation == "change password" {
					changed, err = st.ChangePassword(ctx, user.ID, "old-hash", "new-hash", "expired-session")
				} else {
					changed, err = st.DeleteSelf(ctx, user.ID, "old-hash", "expired-session")
				}
				if err != nil || changed {
					t.Fatalf("expired-session mutation: changed=%v err=%v, want false nil", changed, err)
				}
				assertPasswordHash(t, st, user.ID, "old-hash")
				assertRowCount(t, st, "users", "id", user.ID, 1)
				assertRowCount(t, st, "sessions", "user_id", user.ID, 2)
				gotProfile, _, ok, err := st.ProfileForUser(ctx, user.ID)
				if err != nil || !ok || gotProfile != profileJSON {
					t.Fatalf("ProfileForUser: profile=%q ok=%v err=%v", gotProfile, ok, err)
				}
			})
		})
	}
}

func TestDeleteUserSelfServiceRequiresExpectedHashAndCurrentSession(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expectedHash  string
		revokeSession bool
		wantDeleted   bool
	}{
		{name: "stale password hash", expectedHash: "stale-hash"},
		{name: "revoked session", expectedHash: "old-hash", revokeSession: true},
		{name: "matching credential and session", expectedHash: "old-hash", wantDeleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEachAccountStore(t, func(t *testing.T, st *Store) {
				ctx := context.Background()
				user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
				if err != nil {
					t.Fatalf("CreateUser: %v", err)
				}
				if err := st.CreateSession(ctx, user.ID, "current-session", time.Now().Add(time.Hour)); err != nil {
					t.Fatalf("CreateSession: %v", err)
				}
				if tc.revokeSession {
					if _, err := st.db.ExecContext(ctx, st.query(`DELETE FROM sessions WHERE session_token_hash = ?`), "current-session"); err != nil {
						t.Fatalf("revoke current session: %v", err)
					}
				}

				deleted, err := st.DeleteSelf(ctx, user.ID, tc.expectedHash, "current-session")
				if err != nil || deleted != tc.wantDeleted {
					t.Fatalf("DeleteSelf: deleted=%v err=%v, want %v nil", deleted, err, tc.wantDeleted)
				}
				wantUsers := 1
				if tc.wantDeleted {
					wantUsers = 0
				}
				assertRowCount(t, st, "users", "id", user.ID, wantUsers)
			})
		})
	}
}

func TestResetUserPasswordNormalizesEmailAndRevokesOnlyTargetSessions(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	target, err := st.CreateUser(ctx, "member@example.com", "old-hash")
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	other, err := st.CreateUser(ctx, "other@example.com", "other-hash")
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	for _, session := range []struct {
		userID int64
		hash   string
	}{{target.ID, "target-one"}, {target.ID, "target-two"}, {other.ID, "other-one"}} {
		if err := st.CreateSession(ctx, session.userID, session.hash, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession(%q): %v", session.hash, err)
		}
	}

	updated, err := st.ResetUserPassword(ctx, " MEMBER@EXAMPLE.COM ", "new-hash")
	if err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}
	if updated.ID != target.ID || updated.Email != "member@example.com" || updated.PasswordHash != "new-hash" {
		t.Fatalf("updated user = %#v", updated)
	}
	assertRowCount(t, st, "sessions", "user_id", target.ID, 0)
	assertRowCount(t, st, "sessions", "user_id", other.ID, 1)
	assertPasswordHash(t, st, other.ID, "other-hash")
}

func TestResetUserPasswordRollsBackWhenSessionRevocationFails(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.CreateSession(ctx, user.ID, "session", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	installRejectingSessionDeleteTrigger(t, st)

	if _, err := st.ResetUserPassword(ctx, user.Email, "new-hash"); err == nil {
		t.Fatal("ResetUserPassword error = nil, want session revocation failure")
	}
	assertPasswordHash(t, st, user.ID, "old-hash")
}

func TestDeleteUserCascadesPrivateStateAndPreservesSharedState(t *testing.T) {
	st := newPostgresTestStore(t)
	ctx := context.Background()
	target, err := st.CreateUser(ctx, "delete@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateUser target: %v", err)
	}
	survivor, err := st.CreateUser(ctx, "keep@example.com", "hash")
	if err != nil {
		t.Fatalf("CreateUser survivor: %v", err)
	}
	postingID, _, err := st.UpsertPosting(ctx, samplePosting())
	if err != nil {
		t.Fatalf("UpsertPosting: %v", err)
	}
	if err := seedAccountLifecycleState(ctx, st, target.ID, postingID); err != nil {
		t.Fatalf("seed target state: %v", err)
	}
	if err := seedAccountLifecycleState(ctx, st, survivor.ID, postingID); err != nil {
		t.Fatalf("seed survivor state: %v", err)
	}
	if err := seedSharedAccountLifecycleState(ctx, st, postingID); err != nil {
		t.Fatalf("seed shared state: %v", err)
	}

	deleted, err := st.DeleteUser(ctx, target.ID+survivor.ID+1000)
	if err != nil || deleted {
		t.Fatalf("DeleteUser missing = deleted:%v err:%v, want false nil", deleted, err)
	}
	deleted, err = st.DeleteUser(ctx, target.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteUser target = deleted:%v err:%v, want true nil", deleted, err)
	}

	for _, table := range []string{
		"sessions", "profiles", "bookmarks", "not_interested", "scores",
		"ai_scores", "ai_usage", "user_ai_credentials", "local_data_imports",
		"ai_dealbreaker_validations",
	} {
		assertRowCount(t, st, table, "user_id", target.ID, 0)
		assertRowCount(t, st, table, "user_id", survivor.ID, 1)
	}
	assertRowCount(t, st, "users", "id", survivor.ID, 1)
	assertRowCount(t, st, "postings", "id", postingID, 1)
	assertRowCount(t, st, "ai_extractions", "posting_id", postingID, 1)
	assertTotalRowCount(t, st, "scrape_runs", 1)
}

func TestPostgresAccountMutationInterleavings(t *testing.T) {
	for _, tc := range []struct {
		name         string
		winner       string
		stale        string
		wantHash     string
		wantSessions int
	}{
		{name: "change versus change", winner: "change", stale: "change", wantHash: "winner-hash", wantSessions: 1},
		{name: "reset versus change", winner: "reset", stale: "change", wantHash: "reset-hash", wantSessions: 0},
		{name: "reset versus delete", winner: "reset", stale: "delete", wantHash: "reset-hash", wantSessions: 0},
		{name: "change versus delete", winner: "change", stale: "delete", wantHash: "winner-hash", wantSessions: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newPostgresTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			for _, session := range []string{"winner-session", "stale-session"} {
				if err := st.CreateSession(ctx, user.ID, session, time.Now().Add(time.Hour)); err != nil {
					t.Fatalf("CreateSession(%q): %v", session, err)
				}
			}
			if _, err := st.db.ExecContext(ctx, `
INSERT INTO profiles (user_id, profile_json, profile_hash, updated_at)
VALUES ($1, '{}', 'profile', now())`, user.ID); err != nil {
				t.Fatalf("seed profile: %v", err)
			}

			gateTx, err := st.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin lock gate: %v", err)
			}
			defer gateTx.Rollback()
			var blockerPID int
			if err := gateTx.QueryRowContext(ctx, `
SELECT pg_backend_pid()
  FROM sessions
 WHERE user_id = $1
   AND session_token_hash = 'stale-session'
   FOR UPDATE`, user.ID).Scan(&blockerPID); err != nil {
				t.Fatalf("lock session gate: %v", err)
			}

			winnerResult := make(chan accountMutationResult, 1)
			go func() {
				if tc.winner == "change" {
					changed, err := st.ChangePassword(ctx, user.ID, "old-hash", "winner-hash", "winner-session")
					winnerResult <- accountMutationResult{changed: changed, err: err}
					return
				}
				_, err := st.ResetUserPassword(ctx, user.Email, "reset-hash")
				winnerResult <- accountMutationResult{changed: err == nil, err: err}
			}()
			winnerPID := waitForPostgresBlocker(t, st.db, blockerPID)

			staleResult := make(chan accountMutationResult, 1)
			go func() {
				if tc.stale == "change" {
					changed, err := st.ChangePassword(ctx, user.ID, "old-hash", "stale-hash", "stale-session")
					staleResult <- accountMutationResult{changed: changed, err: err}
					return
				}
				deleted, err := st.DeleteSelf(ctx, user.ID, "old-hash", "stale-session")
				staleResult <- accountMutationResult{changed: deleted, err: err}
			}()
			waitForPostgresBlocker(t, st.db, winnerPID)

			if err := gateTx.Commit(); err != nil {
				t.Fatalf("release lock gate: %v", err)
			}

			winner := receiveMutationResult(t, "winner", winnerResult)
			if winner.err != nil || !winner.changed {
				t.Fatalf("winner mutation: changed=%v err=%v, want true nil", winner.changed, winner.err)
			}
			stale := receiveMutationResult(t, "stale", staleResult)
			if stale.err != nil || stale.changed {
				t.Fatalf("stale mutation: changed=%v err=%v, want false nil", stale.changed, stale.err)
			}
			assertPasswordHash(t, st, user.ID, tc.wantHash)
			assertRowCount(t, st, "sessions", "user_id", user.ID, tc.wantSessions)
			if tc.winner == "change" {
				assertRowCount(t, st, "sessions", "session_token_hash", "winner-session", 1)
				assertRowCount(t, st, "sessions", "session_token_hash", "stale-session", 0)
			}
			assertRowCount(t, st, "users", "id", user.ID, 1)
			assertRowCount(t, st, "profiles", "user_id", user.ID, 1)
		})
	}
}

func TestPostgresAccountMutationsRejectSessionExpiredWhileWaitingForLock(t *testing.T) {
	for _, mutation := range []string{"change password", "delete self"} {
		t.Run(mutation, func(t *testing.T) {
			st := newPostgresTestStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			user, err := st.CreateUser(ctx, "member@example.com", "old-hash")
			if err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			if err := st.CreateSession(ctx, user.ID, "current-session", time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			const profileJSON = `{"career_years":0}`
			if _, _, err := st.SaveProfileForUser(ctx, user.ID, profileJSON); err != nil {
				t.Fatalf("SaveProfileForUser: %v", err)
			}

			gateTx, err := st.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin lock gate: %v", err)
			}
			defer gateTx.Rollback()
			var gatePID int
			if err := gateTx.QueryRowContext(ctx, `
SELECT pg_backend_pid()
  FROM sessions
 WHERE user_id = $1
   AND session_token_hash = 'current-session'
   FOR UPDATE`, user.ID).Scan(&gatePID); err != nil {
				t.Fatalf("lock session gate: %v", err)
			}

			result := make(chan accountMutationResult, 1)
			go func() {
				if mutation == "change password" {
					changed, err := st.ChangePassword(ctx, user.ID, "old-hash", "new-hash", "current-session")
					result <- accountMutationResult{changed: changed, err: err}
					return
				}
				deleted, err := st.DeleteSelf(ctx, user.ID, "old-hash", "current-session")
				result <- accountMutationResult{changed: deleted, err: err}
			}()
			waitForPostgresBlocker(t, st.db, gatePID)

			if _, err := gateTx.ExecContext(ctx, `
UPDATE sessions
   SET expires_at = clock_timestamp()
 WHERE user_id = $1
   AND session_token_hash = 'current-session'`, user.ID); err != nil {
				t.Fatalf("expire locked session: %v", err)
			}
			if err := gateTx.Commit(); err != nil {
				t.Fatalf("release lock gate: %v", err)
			}

			got := receiveMutationResult(t, mutation, result)
			if got.err != nil || got.changed {
				t.Fatalf("expired mutation: changed=%v err=%v, want false nil", got.changed, got.err)
			}
			assertPasswordHash(t, st, user.ID, "old-hash")
			assertRowCount(t, st, "users", "id", user.ID, 1)
			assertRowCount(t, st, "sessions", "user_id", user.ID, 1)
			gotProfile, _, ok, err := st.ProfileForUser(ctx, user.ID)
			if err != nil || !ok || gotProfile != profileJSON {
				t.Fatalf("ProfileForUser: profile=%q ok=%v err=%v", gotProfile, ok, err)
			}
		})
	}
}

func waitForPostgresBlocker(t *testing.T, db *sql.DB, blockerPID int) int {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blockedPID int
		err := db.QueryRow(`
SELECT pid
  FROM pg_stat_activity
 WHERE $1 = ANY(pg_blocking_pids(pid))
 ORDER BY pid
 LIMIT 1`, blockerPID).Scan(&blockedPID)
		if err == nil {
			return blockedPID
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("inspect blocked PostgreSQL query: %v", err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("no mutation blocked behind PostgreSQL backend %d", blockerPID)
		case <-ticker.C:
		}
	}
}

func receiveMutationResult(t *testing.T, name string, result <-chan accountMutationResult) accountMutationResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("%s mutation did not complete", name)
		return accountMutationResult{}
	}
}

func forEachAccountStore(t *testing.T, test func(*testing.T, *Store)) {
	t.Helper()
	t.Run("SQLite", func(t *testing.T) { test(t, newTestStore(t)) })
	t.Run("PostgreSQL", func(t *testing.T) { test(t, newPostgresTestStore(t)) })
}

func installRejectingSessionDeleteTrigger(t *testing.T, st *Store) {
	t.Helper()
	if _, err := st.db.Exec(`
CREATE FUNCTION reject_session_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'reject session delete';
END;
$$;
CREATE TRIGGER reject_session_delete
BEFORE DELETE ON sessions
FOR EACH ROW EXECUTE FUNCTION reject_session_delete()`); err != nil {
		t.Fatalf("install rejecting session trigger: %v", err)
	}
}

func assertPasswordHash(t *testing.T, st *Store, userID int64, want string) {
	t.Helper()
	var got string
	if err := st.db.QueryRow(st.query(`SELECT password_hash FROM users WHERE id = ?`), userID).Scan(&got); err != nil {
		t.Fatalf("query password hash: %v", err)
	}
	if got != want {
		t.Fatalf("password hash = %q, want %q", got, want)
	}
}

func assertRowCount(t *testing.T, st *Store, table, column string, value any, want int) {
	t.Helper()
	var got int
	query := st.query(fmt.Sprintf("SELECT count(*) FROM %s WHERE %s = ?", table, column))
	if err := st.db.QueryRow(query, value).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertTotalRowCount(t *testing.T, st *Store, table string, want int) {
	t.Helper()
	var got int
	if err := st.db.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func seedAccountLifecycleState(ctx context.Context, st *Store, userID, postingID int64) error {
	if err := st.CreateSession(ctx, userID, fmt.Sprintf("delete-session-%d", userID), time.Now().Add(time.Hour)); err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO profiles (user_id, profile_json, profile_hash, updated_at) VALUES ($1, '{}', 'profile', now())`, []any{userID}},
		{`INSERT INTO bookmarks (user_id, posting_id, bookmarked_at) VALUES ($1, $2, now())`, []any{userID, postingID}},
		{`INSERT INTO not_interested (user_id, posting_id, muted_at) VALUES ($1, $2, now())`, []any{userID, postingID}},
		{`INSERT INTO scores (user_id, posting_id, profile_hash, total, breakdown_json, computed_at) VALUES ($1, $2, 'profile', 50, '{}', now())`, []any{userID, postingID}},
		{`INSERT INTO ai_scores (user_id, posting_id, ai_input_hash, ai_version, items_json, net_delta, computed_at) VALUES ($1, $2, 'input', 'version', '[]', 0, now())`, []any{userID, postingID}},
		{`INSERT INTO ai_usage (user_id, day, input_tokens, output_tokens) VALUES ($1, CURRENT_DATE, 1, 1)`, []any{userID}},
		{`INSERT INTO user_ai_credentials (user_id, provider, ciphertext, nonce, encryption_version) VALUES ($1, 'anthropic', $2, $3, 1)`, []any{userID, bytes.Repeat([]byte{1}, 17), bytes.Repeat([]byte{2}, 12)}},
		{`INSERT INTO local_data_imports (user_id, source_sha256, source_counts, imported_counts) VALUES ($1, $2, '{}'::jsonb, '{}'::jsonb)`, []any{userID, strings.Repeat("a", 64)}},
		{`INSERT INTO ai_dealbreaker_validations (user_id, posting_id, content_hash, ai_version, keyword_hash, verdict, evidence, computed_at) VALUES ($1, $2, 'content', 'version', 'keyword', 'applies', 'evidence', now())`, []any{userID, postingID}},
	}
	for _, statement := range statements {
		if _, err := st.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func seedSharedAccountLifecycleState(ctx context.Context, st *Store, postingID int64) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO ai_extractions (posting_id, content_hash, ai_version, min_career, newcomer, education_enum, career_evidence, education_evidence, computed_at) VALUES ($1, 'content', 'version', 0, true, 'none', 'career', 'education', now())`, []any{postingID}},
		{`INSERT INTO scrape_runs (trigger, status, started_at) VALUES ('manual', 'completed', now())`, nil},
	}
	for _, statement := range statements {
		if _, err := st.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}
