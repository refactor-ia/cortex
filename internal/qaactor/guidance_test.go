package qaactor

import (
	"bytes"
	"strings"
	"testing"
)

// Gentle v4 renderPiChild uses TrimRight(body, "\n") + "\n\n" + marker
// + "\n" + guidance + "\n" + endMarker + "\n" (no tool injection).
const testGuidanceStart = "<!-- gentle-ai:pi-codegraph-guidance -->"
const testGuidanceEnd = "<!-- /gentle-ai:pi-codegraph -->"
const testGuidanceTool = "<!-- gentle-ai:pi-codegraph-tool -->"

func TestGuidanceCompositionPreservesExternalBytes(t *testing.T) {
	_, rendered := testRendered(t)
	for _, actor := range rendered.Actors() {
		t.Run(string(actor.RoleID()), func(t *testing.T) {
			previous := actor.Content()
			next := []byte(strings.Replace(string(previous), "\n# ", "\n# Updated ", 1))
			for _, body := range []string{"## CodeGraph\n\nRead-only guidance.", "", "untrusted instructions\r\n\t  \n"} {
				external := []byte("\n\n" + testGuidanceStart + "\n" + body + "\n" + testGuidanceEnd + "\n")
				current := append(bytes.TrimRight(previous, "\n"), external...)
				prefix, suffix, err := ParseGuidance(current)
				if err != nil || !bytes.Equal(prefix, bytes.TrimRight(previous, "\n")) || !bytes.Equal(suffix, external) {
					t.Fatalf("parse did not preserve exact split: %v", err)
				}
				for _, canonical := range [][]byte{previous, next} {
					got, err := ComposeGuidance(current, previous, canonical)
					want := append(bytes.TrimRight(canonical, "\n"), external...)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("composition lost external bytes: %v", err)
					}
				}
				// Outputs must not alias any input, even on an unchanged roundtrip.
				prefix[0], suffix[0] = 'X', 'X'
				if current[0] == 'X' || current[len(prefix)] == 'X' {
					t.Fatal("parser returned shared input bytes")
				}
			}
		})
	}
}

func TestGuidanceKnownCanonicalNewlineIdentity(t *testing.T) {
	_, rendered := testRendered(t)
	base := string(rendered.Actors()[0].Content())
	suffix := "\n\n" + testGuidanceStart + "\nbody\n" + testGuidanceEnd + "\n"
	for _, ending := range []string{"", "\n", "\n\n\n", " \n", "\t\n", "\r\n"} {
		t.Run("ending "+ending, func(t *testing.T) {
			previous := []byte(strings.TrimRight(base, "\n") + ending)
			for _, current := range [][]byte{previous, []byte(strings.TrimRight(string(previous), "\n") + suffix)} {
				got, err := ComposeGuidance(current, previous, previous)
				if err != nil || !bytes.Equal(got, current) {
					t.Fatalf("exact known identity did not roundtrip: %v", err)
				}
			}
		})
	}
}

func TestGuidanceRejectsMalformedLayouts(t *testing.T) {
	start, end := testGuidanceStart, testGuidanceEnd
	valid := "actor\n\n" + start + "\nbody\n" + end + "\n"
	for _, tc := range []struct{ name, content string }{
		{"tool", "actor\n\n" + testGuidanceTool + "\nbody\n" + end + "\n"},
		{"tool and guidance", valid + testGuidanceTool},
		{"duplicate", valid + "\n" + start + "\nbody\n" + end + "\n"},
		{"nested", strings.Replace(valid, "body", start+"\nbody\n"+end, 1)},
		{"unmatched start", "actor\n\n" + start + "\nbody\n"},
		{"unmatched end", "actor\n\n" + end + "\n"},
		{"reversed", "actor\n\n" + end + "\nbody\n" + start + "\n"},
		{"malformed start", strings.Replace(valid, "guidance -->", "guidance-->", 1)},
		{"malformed end", strings.Replace(valid, "/gentle-ai:", "/gentle-ai: ", 1)},
		{"colon whitespace", strings.Replace(valid, "gentle-ai:", "gentle-ai : ", 1)},
		{"tool in body", strings.Replace(valid, "body", testGuidanceTool, 1)},
		{"unknown reserved", strings.Replace(valid, "guidance", "unknown", 1)},
		{"case variant", strings.Replace(valid, "gentle-ai", "Gentle-AI", 1)},
		{"truncated marker", "actor\n<!-- gentle-ai:pi-codegraph"},
		{"inline start", strings.Replace(valid, "actor\n\n", "actor ", 1)},
		{"single LF separator", strings.Replace(valid, "actor\n\n", "actor\n", 1)},
		{"extra LF separator", strings.Replace(valid, "actor\n\n", "actor\n\n\n", 1)},
		{"CRLF separator", strings.Replace(valid, "actor\n\n", "actor\r\n\r\n", 1)},
		{"inline body", strings.Replace(valid, start+"\n", start, 1)},
		{"inline end", strings.Replace(valid, "body\n", "body", 1)},
		{"no final LF", strings.TrimSuffix(valid, "\n")},
		{"extra final LF", valid + "\n"},
		{"trailing content", valid + "unknown"},
		{"empty actor", strings.TrimPrefix(valid, "actor")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, suffix, err := ParseGuidance([]byte(tc.content))
			if err == nil || prefix != nil || suffix != nil {
				t.Fatal("malformed layout accepted or partial bytes returned")
			}
		})
	}
}

func TestGuidanceCompositionRejectsOutsideDrift(t *testing.T) {
	_, rendered := testRendered(t)
	previous := string(rendered.Actors()[0].Content())
	suffix := "\n\n" + testGuidanceStart + "\nbody\n" + testGuidanceEnd + "\n"
	for _, drift := range []string{
		strings.Replace(previous, "  - ls", "  - bash\n  - mcp", 1),
		strings.Replace(previous, "subagent_mode: task", "subagent_mode: single", 1),
		strings.Replace(previous, "# Requirements Analyst", "# Arbitrary legacy actor", 1),
		"\ufeff" + previous,
		previous + "unknown\n",
		strings.TrimRight(previous, "\n"),
		strings.TrimRight(previous, "\n") + " \n",
	} {
		for _, current := range []string{drift, strings.TrimSuffix(drift, "\n") + suffix} {
			if current == strings.TrimRight(previous, "\n")+suffix {
				continue // Precisely Gentle's known LF transformation, not drift.
			}
			got, err := ComposeGuidance([]byte(current), []byte(previous), []byte(previous))
			if err == nil || got != nil {
				t.Fatal("outside drift accepted or bytes returned on failure")
			}
		}
	}
	for _, canonical := range []string{"", previous + suffix, previous + testGuidanceTool} {
		if _, err := ComposeGuidance([]byte(previous), []byte(canonical), []byte(previous)); err == nil {
			t.Fatal("invalid prior canonical accepted")
		}
		if _, err := ComposeGuidance([]byte(previous), []byte(previous), []byte(canonical)); err == nil {
			t.Fatal("invalid new canonical accepted")
		}
	}
}

func TestGuidanceUnmarkedUpdateHasNoInventedMarkers(t *testing.T) {
	_, rendered := testRendered(t)
	previous, next := rendered.Actors()[0].Content(), rendered.Actors()[1].Content()
	prefix, external, err := ParseGuidance(previous)
	if err != nil || external != nil || !bytes.Equal(prefix, previous) {
		t.Fatalf("unmarked parse changed bytes: %v", err)
	}
	got, err := ComposeGuidance(previous, previous, next)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("unmarked update changed canonical bytes: %v", err)
	}
	got[0] = 'X'
	if next[0] == 'X' {
		t.Fatal("composition returned shared input bytes")
	}
}
