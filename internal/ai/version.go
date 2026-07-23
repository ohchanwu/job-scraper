package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	extractionContractRevision = "1"
	DealbreakerPromptVersion   = "1"
	ScorePromptVersion         = "1"
)

func taskVersion(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}

func ExtractionContractVersion() string {
	return taskVersion("extraction-contract", extractionContractRevision)
}

func DealbreakerVersion(provider, model string) string {
	return taskVersion(provider, model, "dealbreaker", DealbreakerPromptVersion)
}

// ScoreVersion preserves the original Stage-2 cache identity.
func ScoreVersion(provider, model string) string {
	return taskVersion(provider, model, ScorePromptVersion)
}

// AIVersion is the Stage-2 compatibility alias.
func AIVersion(provider, model string) string {
	return ScoreVersion(provider, model)
}
