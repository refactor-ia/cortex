package qaactor

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
)

const (
	externalGuidanceStart = "<!-- gentle-ai:pi-codegraph-guidance -->"
	externalGuidanceEnd   = "<!-- /gentle-ai:pi-codegraph -->"
)

// Reserve the CodeGraph namespace, including misspelled/truncated comments,
// case variants and whitespace around the colon. Tool markers are never valid.
var reservedGuidance = regexp.MustCompile(`(?i)gentle-ai\s*:\s*pi-codegraph`)

// ParseGuidance splits an optional external Gentle suffix from an actor prefix.
// The suffix includes its exact two-LF separator and final LF. Both outputs are
// copies. With no suffix, the prefix is the entire input, unchanged.
//
// Only Gentle v4's guidance-only renderPiChild layout is supported. The prefix
// lacks canonical trailing LFs removed by that renderer; it is NOT an identity.
// Markers establish structure, never ownership or authenticity. Guidance may
// contain arbitrary untrusted text. Callers must enforce a full effective hash
// and explicit adoption gate before using these bytes as an admitted prompt.
func ParseGuidance(content []byte) (prefix, external []byte, err error) {
	text := string(content)
	remainder := strings.ReplaceAll(text, externalGuidanceStart, "")
	remainder = strings.ReplaceAll(remainder, externalGuidanceEnd, "")
	if reservedGuidance.MatchString(remainder) {
		return nil, nil, errors.New("unsupported or malformed external CodeGraph marker")
	}
	starts, ends := strings.Count(text, externalGuidanceStart), strings.Count(text, externalGuidanceEnd)
	if starts == 0 && ends == 0 {
		return append([]byte(nil), content...), nil, nil
	}
	if starts != 1 || ends != 1 {
		return nil, nil, errors.New("external guidance requires exactly one marker pair")
	}
	start, end := strings.Index(text, externalGuidanceStart), strings.Index(text, externalGuidanceEnd)
	bodyStart := start + len(externalGuidanceStart)
	if start < 3 || text[start-2:start] != "\n\n" || text[start-3] == '\n' ||
		end <= bodyStart+1 || text[bodyStart] != '\n' || text[end-1] != '\n' ||
		end+len(externalGuidanceEnd)+1 != len(text) || text[len(text)-1] != '\n' {
		return nil, nil, errors.New("unexpected external guidance suffix layout")
	}
	return append([]byte(nil), content[:start-2]...), append([]byte(nil), content[start-2:]...), nil
}

// ComposeGuidance replaces only a known prior canonical actor, preserving an
// existing external suffix byte-for-byte. It never inserts markers or adopts
// an arbitrary legacy body. Canonical arguments must be nonempty and unmarked;
// callers supply them from trusted renders, not from the parsed current prefix.
//
// Without guidance, current must equal previousCanonical exactly. With guidance,
// compare against only the known source's trailing-LF transformation performed
// by Gentle v4. Never trim current whitespace or canonical spaces/tabs/CRs.
// Output is a fresh copy. This proves only canonical equality, NOT authority to
// trust the external block; full effective identity/adoption belongs to callers.
// This is not an uninstall helper: external material must not be stripped.
func ComposeGuidance(current, previousCanonical, newCanonical []byte) ([]byte, error) {
	for _, canonical := range [][]byte{previousCanonical, newCanonical} {
		_, external, err := ParseGuidance(canonical)
		if err != nil || len(canonical) == 0 || external != nil {
			return nil, errors.New("canonical actor must be nonempty and unmarked")
		}
	}
	prefix, external, err := ParseGuidance(current)
	if err != nil {
		return nil, err
	}
	expected, replacement := previousCanonical, newCanonical
	if external != nil {
		// Normalize only known canonical bytes, exactly as the external renderer.
		expected = bytes.TrimRight(previousCanonical, "\n")
		replacement = bytes.TrimRight(newCanonical, "\n")
	}
	if !bytes.Equal(prefix, expected) {
		return nil, errors.New("current actor differs from known prior canonical identity")
	}
	result := append([]byte(nil), replacement...)
	return append(result, external...), nil
}
