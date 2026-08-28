package backupjournal

import (
	"encoding/json"
	"strings"
	"testing"
)

const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validEntries() []EntryInput {
	return []EntryInput{
		{Runtime: RuntimeClaude, Root: RootClaude, RelativePath: "settings.json", Existence: Present, Mode: 0600, SHA256: hash, Length: 0},
		{Runtime: RuntimePi, Root: RootPi, RelativePath: "a/config.json", Existence: Absent},
		{Runtime: RuntimeOpenCode, Root: RootOpenCode, RelativePath: "config.json", Existence: Present, Mode: 0644, SHA256: hash, Length: 3},
	}
}

func journal(t *testing.T) Manifest {
	t.Helper()
	journal, err := New(hash, hash, validEntries())
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestNewCanonicalizesAllRuntimeEntries(t *testing.T) {
	journal := journal(t)
	if journal.State() != Prepared || journal.TransactionID() != hash || journal.CandidateFingerprint() != hash {
		t.Fatal("prepared identity was not retained")
	}
	entries := journal.Entries()
	if len(entries) != 3 || entries[0].Runtime != RuntimePi || entries[1].Runtime != RuntimeOpenCode || entries[2].Runtime != RuntimeClaude {
		t.Fatalf("unexpected order: %#v", entries)
	}
	if entries[0].BlobName != "" || entries[0].Mode != 0 || entries[0].SHA256 != "" {
		t.Fatal("absent entry metadata is not canonical")
	}
	if entries[1].BlobName != "blobs/opencode/config.json" || entries[2].BlobName != "blobs/claude-code/settings.json" {
		t.Fatal("blob names are not deterministic")
	}
}

func TestTransitionIsOneWayAndImmutable(t *testing.T) {
	journal := journal(t)
	for _, state := range []State{Committed, Recovered} {
		next, err := journal.Transition(state)
		if err != nil || next.State() != state || journal.State() != Prepared {
			t.Fatalf("%s: transition=%v err=%v", state, next.State(), err)
		}
		if _, err := next.Transition(state); err == nil {
			t.Fatalf("%s replay succeeded", state)
		}
	}
	if _, err := journal.Transition(Prepared); err == nil {
		t.Fatal("prepared replay succeeded")
	}
}

func TestNewRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name   string
		change func([]EntryInput)
	}{
		{"missing runtime", func(e []EntryInput) { e[0].Runtime = "other" }},
		{"root mismatch", func(e []EntryInput) { e[0].Root = RootPi }},
		{"traversal", func(e []EntryInput) { e[0].RelativePath = "../settings" }},
		{"absolute path", func(e []EntryInput) { e[0].RelativePath = "/private/settings" }},
		{"noncanonical separator", func(e []EntryInput) { e[0].RelativePath = "a\\b" }},
		{"dot path", func(e []EntryInput) { e[0].RelativePath = "." }},
		{"duplicate destination", func(e []EntryInput) { e[2].Runtime, e[2].Root, e[2].RelativePath = RuntimePi, RootPi, "a/config.json" }},
		{"bad mode", func(e []EntryInput) { e[1].Mode = 0777 }},
		{"bad hash", func(e []EntryInput) { e[1].SHA256 = strings.ToUpper(hash) }},
		{"negative length", func(e []EntryInput) { e[1].Length = -1 }},
		{"maximum length", func(e []EntryInput) { e[1].Length = maxLength + 1 }},
		{"absent metadata", func(e []EntryInput) { e[1].SHA256 = hash }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			entries := validEntries()
			test.change(entries)
			if _, err := New(hash, hash, entries); err == nil {
				t.Fatal("accepted invalid entry")
			}
		})
	}
}

func TestNewRejectsOverlappingDestinations(t *testing.T) {
	for _, relativePath := range []string{"a", "a/config.json/child"} {
		t.Run(relativePath, func(t *testing.T) {
			entries := append(validEntries(), EntryInput{Runtime: RuntimePi, Root: RootPi, RelativePath: relativePath, Existence: Absent})
			if _, err := New(hash, hash, entries); err == nil {
				t.Fatal("accepted overlapping destination")
			}
		})
	}
}

func TestNewRejectsInvalidIdentityAndMissingRuntime(t *testing.T) {
	for _, value := range []string{"", strings.ToUpper(hash), "abc"} {
		if _, err := New(value, hash, validEntries()); err == nil {
			t.Fatal("accepted invalid transaction ID")
		}
	}
	for _, entries := range [][]EntryInput{nil, validEntries()[:2], make([]EntryInput, maxEntries+1)} {
		if _, err := New(hash, hash, entries); err == nil {
			t.Fatal("accepted invalid entry bounds or missing runtime")
		}
	}
}

func TestNewAllowsSamePathAcrossRuntimes(t *testing.T) {
	entries := validEntries()
	for index := range entries {
		entries[index].RelativePath = "config.json"
	}
	if _, err := New(hash, hash, entries); err != nil {
		t.Fatal(err)
	}
}

func TestJSONIsStrictCanonicalAndDetached(t *testing.T) {
	journal := journal(t)
	if journal.Recoverable() || journal.Version() != SchemaVersion || len(journal.RootBindings()) != 0 || journal.Entries()[0].AfterEvidence().Existence != "" {
		t.Fatal("v1 journal exposed recoverable metadata")
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(encoded)
	if err != nil || strings.Contains(string(encoded), "/Users/") || string(encoded) != string(mustJSON(t, parsed)) {
		t.Fatalf("parse err=%v", err)
	}
	committed, err := journal.Transition(Committed)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip, err := Parse(mustJSON(t, committed)); err != nil || roundTrip.State() != Committed {
		t.Fatalf("terminal parse err=%v", err)
	}
	for _, replacement := range [][]byte{
		append([]byte(`{"unknown":true,`), encoded[1:]...),
		append(encoded, '\n'),
		[]byte(`{"schemaVersion":2}`),
		[]byte(strings.Replace(string(encoded), `"state":"prepared"`, `"state":"prepared","state":"prepared"`, 1)),
	} {
		if _, err := Parse(replacement); err == nil {
			t.Fatalf("accepted %s", replacement)
		}
	}
	entries := parsed.Entries()
	entries[0].RelativePath = "changed"
	if parsed.Entries()[0].RelativePath == "changed" {
		t.Fatal("entries leaked mutable storage")
	}
}

func TestParseRejectsMalformedStoredEntry(t *testing.T) {
	encoded := string(mustJSON(t, journal(t)))
	for _, replacement := range []string{"/Users/example/.config", "blobs/opencode/a/../config.json", "blobs/opencode/wrong.json"} {
		bad := []byte(strings.Replace(encoded, "blobs/opencode/config.json", replacement, 1))
		if _, err := Parse(bad); err == nil {
			t.Fatalf("accepted blob %q", replacement)
		}
	}
}

func recoverableRoots(t *testing.T) []RootBinding {
	t.Helper()
	roots := make([]RootBinding, 0, 3)
	for _, root := range []struct {
		runtime Runtime
		kind    RootKind
		path    string
	}{{RuntimePi, RootPi, "/private/pi"}, {RuntimeOpenCode, RootOpenCode, "/private/opencode"}, {RuntimeClaude, RootClaude, "/private/claude"}} {
		binding, err := NewRootBinding(root.runtime, root.kind, root.path)
		if err != nil || !binding.MatchesRoot(root.path) || binding.MatchesRoot(root.path+"-other") {
			t.Fatalf("root binding err=%v", err)
		}
		roots = append(roots, binding)
	}
	same, _ := NewRootBinding(RuntimePi, RootPi, "/private/pi")
	different, _ := NewRootBinding(RuntimePi, RootPi, "/private/pi-other")
	if roots[0].Digest() != same.Digest() || roots[0].Digest() == different.Digest() {
		t.Fatal("root digest was not root-specific and stable")
	}
	return roots
}

func recoverableEntries() []RecoverableEntryInput {
	return []RecoverableEntryInput{
		{Before: EntryInput{Runtime: RuntimePi, Root: RootPi, RelativePath: "config.json", Existence: Absent}, After: Evidence{Existence: Present, Mode: 0600, SHA256: hash, Length: 1}},
		{Before: EntryInput{Runtime: RuntimeOpenCode, Root: RootOpenCode, RelativePath: "config.json", Existence: Present, Mode: 0644, SHA256: hash, Length: 1}, After: Evidence{Existence: Absent}},
		{Before: EntryInput{Runtime: RuntimeClaude, Root: RootClaude, RelativePath: "config.json", Existence: Present, Mode: 0600, SHA256: hash, Length: 1}, After: Evidence{Existence: Present, Mode: 0644, SHA256: strings.Repeat("f", 64), Length: 2}},
	}
}

func TestRecoverableJournalBindsRootsAndAfterEvidence(t *testing.T) {
	journal, err := NewRecoverable(hash, hash, recoverableRoots(t), recoverableEntries())
	if err != nil || !journal.Recoverable() || journal.Version() != 2 {
		t.Fatalf("recoverable journal err=%v", err)
	}
	encoded := mustJSON(t, journal)
	if strings.Contains(string(encoded), "/private/") {
		t.Fatal("manifest disclosed a root path")
	}
	parsed, err := Parse(encoded)
	if err != nil || !parsed.Recoverable() || len(parsed.RootBindings()) != 3 || parsed.Entries()[0].AfterEvidence().Existence != Present {
		t.Fatalf("recoverable parse err=%v", err)
	}
}

func TestRecoverableRejectsNoopAndUnorderedEvidence(t *testing.T) {
	entries := recoverableEntries()
	entries[0].After = Evidence{}
	if _, err := NewRecoverable(hash, hash, recoverableRoots(t), entries); err == nil {
		t.Fatal("accepted absent-to-absent no-op")
	}
	entries = recoverableEntries()
	entries[0], entries[1] = entries[1], entries[0]
	if _, err := NewRecoverable(hash, hash, recoverableRoots(t), entries); err == nil {
		t.Fatal("accepted noncanonical entry ordering")
	}
	roots := recoverableRoots(t)
	roots[0], roots[1] = roots[1], roots[0]
	if _, err := NewRecoverable(hash, hash, roots, recoverableEntries()); err == nil {
		t.Fatal("accepted noncanonical root ordering")
	}
}

func mustJSON(t *testing.T, journal Manifest) []byte {
	t.Helper()
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
