package qapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// Backends and versions the fixture tree under testdata/ was captured from.
const (
	piFixtureBackend = "pi"
	piFixtureVersion = "0.85.1"

	claudeFixtureBackend = "claude"
	claudeFixtureVersion = "2.1.278"

	opencodeFixtureBackend = "opencode"
	opencodeFixtureVersion = "1.18.25"
)

// The helpers below are shared by every backend stream suite: each test
// starts from a real committed capture and mutates it, rather than hand-writing
// a shape the CLI may never emit.

// backendTestdata builds a path into the fixture tree of one backend at one
// version. Fixtures live under testdata/<backend>-<version>/ so a second
// backend can be added without a second convention.
func backendTestdata(backend, version string, parts ...string) string {
	segments := make([]string, 0, len(parts)+2)
	segments = append(segments, "testdata", fmt.Sprintf("%s-%s", backend, version))
	segments = append(segments, parts...)
	return filepath.Join(segments...)
}

// streamLines splits one captured stream into its events. The captures are
// JSON Lines and always end with a terminating newline.
func streamLines(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	if len(stream) == 0 || stream[len(stream)-1] != '\n' {
		t.Fatalf("capture is not newline terminated")
	}
	return bytes.Split(stream[:len(stream)-1], []byte("\n"))
}

// joinStream reassembles a JSON Lines stream from events.
func joinStream(lines [][]byte) []byte {
	var stream bytes.Buffer
	for _, line := range lines {
		stream.Write(line)
		stream.WriteByte('\n')
	}
	return stream.Bytes()
}

// streamEventIndex finds the first event of one type in a split capture.
func streamEventIndex(t *testing.T, lines [][]byte, kind string) int {
	t.Helper()
	for index, line := range lines {
		var event map[string]any
		if json.Unmarshal(line, &event) == nil && event["type"] == kind {
			return index
		}
	}
	t.Fatalf("capture carries no %q event", kind)
	return -1
}

// mutateStreamEvent rewrites one event of a split capture through a decoded
// map, so a mutation targets a field rather than a byte sequence of the
// capture.
func mutateStreamEvent(t *testing.T, lines [][]byte, index int, mutate func(map[string]any)) [][]byte {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(lines[index], &event); err != nil {
		t.Fatalf("decode event %d: %v", index, err)
	}
	mutate(event)
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode event %d: %v", index, err)
	}
	mutated := slices.Clone(lines)
	mutated[index] = encoded
	return mutated
}
