package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/ohchanwu/jobcron/internal/ai"
	"github.com/ohchanwu/jobcron/internal/profile"
	"github.com/ohchanwu/jobcron/internal/scraper"
	"github.com/ohchanwu/jobcron/internal/tokenmatch"
	"golang.org/x/text/unicode/norm"
)

const maxDealbreakerEvidenceRunes = 240

// tokenize splits text the way SQLite FTS5's unicode61 tokenizer does: it
// NFC-normalizes the text, breaks it into maximal runs of letters and digits
// (every other character is a separator), and lowercases each token.
//
// This is the v1 matching strategy — see the design doc's Matching Semantics
// and the user's Step 5 decision. It deliberately reproduces FTS5's token-
// exact behavior: "개발" and "개발자" are distinct tokens.
func tokenize(text string) []string { return tokenmatch.Tokenize(text) }

// normalizeText NFC-normalizes and lowercases a string — used for whole-string
// (non-tokenized) comparisons such as location matching.
func normalizeText(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}

// textContains reports whether phrase occurs in text as a contiguous run of
// tokens — the same token-exact, phrase-ordered semantics as an FTS5 quoted
// phrase MATCH. An empty phrase (one that tokenizes to nothing) matches
// nothing.
func textContains(text, phrase string) bool { return tokenmatch.Contains(text, phrase) }

// DealbreakerCandidates returns every matched profile phrase in profile order.
func DealbreakerCandidates(p scraper.Posting, prof profile.Profile) []ai.DealbreakerCandidate {
	text := p.Title + " " + p.Company + " " + p.Description
	var candidates []ai.DealbreakerCandidate
	for _, phrase := range prof.Dealbreakers {
		tokens := tokenize(phrase)
		if len(tokens) == 0 || !textContains(text, phrase) {
			continue
		}
		sum := sha256.Sum256([]byte(strings.Join(tokens, "\x00")))
		candidates = append(candidates, ai.DealbreakerCandidate{
			ID:     hex.EncodeToString(sum[:]),
			Phrase: phrase,
			Match:  canonicalDealbreakerMatch(p, text, phrase),
		})
	}
	return candidates
}

func canonicalDealbreakerMatch(p scraper.Posting, combinedText, phrase string) ai.DealbreakerMatch {
	for _, tag := range p.Tags {
		if tokenmatch.Contains(tag.Name, phrase) && tokenmatch.Contains(p.Description, tag.Name) {
			return ai.DealbreakerMatch{
				Evidence: boundedMatchEvidence(tag.Name, phrase),
				Source:   ai.DealbreakerMatchStructuredTag,
				Category: tag.Category,
			}
		}
	}
	if tokenmatch.Contains(p.Title, phrase) {
		return ai.DealbreakerMatch{Evidence: boundedMatchEvidence(p.Title, phrase), Source: ai.DealbreakerMatchTitle}
	}
	if tokenmatch.Contains(p.Company, phrase) {
		return ai.DealbreakerMatch{Evidence: boundedMatchEvidence(p.Company, phrase), Source: ai.DealbreakerMatchCompany}
	}
	if line, ok := shortestMatchingLine(p.Description, phrase); ok {
		return ai.DealbreakerMatch{Evidence: boundedMatchEvidence(line, phrase), Source: ai.DealbreakerMatchDescription}
	}
	return ai.DealbreakerMatch{Evidence: boundedMatchEvidence(combinedText, phrase), Source: ai.DealbreakerMatchCombined}
}

func shortestMatchingLine(description, phrase string) (string, bool) {
	var shortest string
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if !tokenmatch.Contains(line, phrase) {
			continue
		}
		if shortest == "" || utf8.RuneCountInString(line) < utf8.RuneCountInString(shortest) {
			shortest = line
		}
	}
	return shortest, shortest != ""
}

func boundedMatchEvidence(value, phrase string) string {
	runes := []rune(value)
	if len(runes) <= maxDealbreakerEvidenceRunes {
		return value
	}
	startByte, endByte, ok := tokenmatch.Find(value, phrase)
	if !ok {
		return string(runes[:maxDealbreakerEvidenceRunes])
	}
	matched := value[startByte:endByte]
	if utf8.RuneCountInString(matched) > maxDealbreakerEvidenceRunes {
		// The matcher permits arbitrarily long separator runs between adjacent
		// tokens. Compact only that matched source span so bounded evidence keeps
		// the same tokens without changing matcher semantics.
		return strings.Join(tokenmatch.Tokenize(matched), " ")
	}
	start := utf8.RuneCountInString(value[:startByte])
	end := start + utf8.RuneCountInString(matched)
	windowStart := start - (maxDealbreakerEvidenceRunes-(end-start))/2
	if windowStart < 0 {
		windowStart = 0
	}
	if windowStart+maxDealbreakerEvidenceRunes > len(runes) {
		windowStart = len(runes) - maxDealbreakerEvidenceRunes
	}
	return string(runes[windowStart : windowStart+maxDealbreakerEvidenceRunes])
}
