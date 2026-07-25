package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type rerateState string
type rerateOutcome string

const (
	rerateStateIdle    rerateState = "idle"
	rerateStateRunning rerateState = "running"
	rerateStateDone    rerateState = "done"
	rerateStateFailed  rerateState = "failed"
)

const (
	rerateOutcomeChanged    rerateOutcome = "changed"
	rerateOutcomeCached     rerateOutcome = "cached"
	rerateOutcomePartial    rerateOutcome = "partial"
	rerateOutcomeEmpty      rerateOutcome = "empty"
	rerateOutcomeNoProgress rerateOutcome = "no_progress"
)

type rerateKey struct {
	userID  int64
	surface string
}

type rerateStatus struct {
	RunID      uint64        `json:"run_id"`
	RunToken   string        `json:"run_token,omitempty"`
	OwnerEntry string        `json:"owner_entry,omitempty"`
	State      rerateState   `json:"state"`
	Status     string        `json:"status,omitempty"`
	Progress   string        `json:"progress,omitempty"`
	Message    string        `json:"message,omitempty"`
	Outcome    rerateOutcome `json:"outcome,omitempty"`
}

type rerateTracker struct {
	mu     sync.RWMutex
	nextID uint64
	epoch  string
	runs   map[rerateKey]rerateStatus
}

func newRerateTracker() *rerateTracker {
	return &rerateTracker{epoch: newRerateEpoch(), runs: make(map[rerateKey]rerateStatus)}
}

func newRerateEpoch() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("fallback-%x", time.Now().UnixNano())
}

func (t *rerateTracker) start(userID int64, surface, ownerEntry string) rerateStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	status := rerateStatus{
		RunID:      t.nextID,
		RunToken:   fmt.Sprintf("%s-%d", t.epoch, t.nextID),
		OwnerEntry: ownerEntry,
		State:      rerateStateRunning,
	}
	t.runs[rerateKey{userID: userID, surface: surface}] = status
	return status
}

func validRerateEntryToken(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (t *rerateTracker) record(userID int64, surface string, runID uint64, event, data string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := rerateKey{userID: userID, surface: surface}
	status, ok := t.runs[key]
	if !ok || status.RunID != runID {
		return
	}
	switch event {
	case "status":
		status.Status = data
	case "progress":
		status.Progress = data
	case "failed":
		status.State = rerateStateFailed
		status.Message = data
	default:
		return
	}
	t.runs[key] = status
}

func (t *rerateTracker) complete(userID int64, surface string, runID uint64, outcome rerateOutcome, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := rerateKey{userID: userID, surface: surface}
	status, ok := t.runs[key]
	if !ok || status.RunID != runID {
		return
	}
	status.State = rerateStateDone
	status.Message = message
	status.Outcome = outcome
	t.runs[key] = status
}

func (t *rerateTracker) snapshot(userID int64, surface string) (rerateStatus, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	status, ok := t.runs[rerateKey{userID: userID, surface: surface}]
	return status, ok
}

func (t *rerateTracker) invalidateUser(userID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key := range t.runs {
		if key.userID == userID {
			delete(t.runs, key)
		}
	}
}

func (s *Server) handleRerateStatus(w http.ResponseWriter, r *http.Request) {
	surface := r.URL.Query().Get("surface")
	if !validRerateSurface(surface) {
		http.Error(w, "알 수 없는 화면이에요.", http.StatusBadRequest)
		return
	}
	userID, err := s.stateUserID(r.Context(), r)
	if err != nil {
		writeAuthUnauthorized(w)
		return
	}
	status, ok := s.rerates.snapshot(userID, surface)
	if !ok {
		status = rerateStatus{State: rerateStateIdle}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
