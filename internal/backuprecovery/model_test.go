package backuprecovery

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/refactor-ia/cortex/internal/backupjournal"
)

type modelFixture struct {
	manifest backupjournal.Manifest
	blobs    []beforeBlob
	before   []currentEvidence
	after    []currentEvidence
}

func TestInvalidModelErrorIsStable(t *testing.T) {
	if err := invalidModel(); !errors.Is(err, invalidModelError{}) || err.Error() != "backup recovery: invalid model evidence" {
		t.Fatalf("invalidModel() = %v", err)
	}
}
func TestClassifyStates(t *testing.T) {
	cases := []struct {
		name          string
		before, after backupjournal.Existence
		change        func([]currentEvidence)
		want          classification
	}{
		{"create before", backupjournal.Absent, backupjournal.Present, nil, atBefore},
		{"create after", backupjournal.Absent, backupjournal.Present, nil, atAfter},
		{"replace before", backupjournal.Present, backupjournal.Present, nil, atBefore},
		{"replace after", backupjournal.Present, backupjournal.Present, nil, atAfter},
		{"remove before", backupjournal.Present, backupjournal.Absent, nil, atBefore},
		{"remove after", backupjournal.Present, backupjournal.Absent, nil, atAfter},
		{"drift", backupjournal.Absent, backupjournal.Present, func(v []currentEvidence) { v[0] = newCurrentPresent(v[0].key, 0600, []byte("drift")) }, drifted},
		{"unsafe", backupjournal.Present, backupjournal.Present, func(v []currentEvidence) { v[0] = newCurrentUnsafe(v[0].key) }, unsafe},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := fixture(t, test.before, test.after)
			current := append([]currentEvidence(nil), value.before...)
			if test.want == atAfter {
				current = append([]currentEvidence(nil), value.after...)
			} else if test.change != nil {
				test.change(current)
			}
			assertPlan(t, value, current, test.want, test.want != drifted && test.want != unsafe)
		})
	}
}
func TestClassifyZeroByteTransitions(t *testing.T) {
	cases := []struct {
		name                  string
		before, after         backupjournal.Existence
		beforeData, afterData []byte
		current               string
		want                  classification
	}{
		{"zero-byte create after", backupjournal.Absent, backupjournal.Present, nil, []byte{}, "after", atAfter},
		{"zero-byte replace before", backupjournal.Present, backupjournal.Present, []byte{}, []byte("after"), "before", atBefore},
		{"zero-byte replace after", backupjournal.Present, backupjournal.Present, []byte("before"), []byte{}, "after", atAfter},
		{"zero-byte remove before", backupjournal.Present, backupjournal.Absent, []byte{}, nil, "before", atBefore},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := fixture(t, test.before, test.after, test.beforeData, test.afterData)
			current := value.before
			if test.current == "after" {
				current = value.after
			}
			assertPlan(t, value, current, test.want, true)
		})
	}
}
func TestClassifyDeniesDriftAcrossTransaction(t *testing.T) {
	value := fixture(t, backupjournal.Present, backupjournal.Present)
	current := append([]currentEvidence(nil), value.before...)
	current[1] = newCurrentPresent(current[1].key, 0600, []byte("drift"))
	plan := assertPlan(t, value, current, atBefore, false)
	if plan.entries[1].state != drifted {
		t.Fatalf("second state = %v, want drifted", plan.entries[1].state)
	}
}
func TestClassifyRejectsInvalidManifest(t *testing.T) {
	value := fixture(t, backupjournal.Present, backupjournal.Absent)
	entries := make([]backupjournal.EntryInput, 0, 3)
	for _, runtime := range runtimeList {
		entries = append(entries, entry(runtime, backupjournal.Present, []byte("before")))
	}
	legacy, _ := backupjournal.New(hash("legacy"), hash("candidate"), entries)
	terminal, _ := value.manifest.Transition(backupjournal.Recovered)
	for name, manifest := range map[string]backupjournal.Manifest{"v1": legacy, "terminal": terminal} {
		t.Run(name, func(t *testing.T) { assertInvalid(t, manifest, value.blobs, value.before) })
	}
}
func TestClassifyRejectsUnmatchedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*modelFixture)
	}{
		{"missing current", func(v *modelFixture) { v.before = v.before[1:] }},
		{"extra current", func(v *modelFixture) { v.before = append(v.before, newCurrentAbsent("extra")) }},
		{"duplicate current", func(v *modelFixture) { v.before = append(v.before, v.before[0]) }},
		{"malformed current", func(v *modelFixture) { v.before[0] = currentEvidence{key: v.before[0].key, state: currentPresent} }},
		{"missing blob", func(v *modelFixture) { v.blobs = v.blobs[1:] }},
		{"extra blob", func(v *modelFixture) { v.blobs = append(v.blobs, newBeforeBlob("extra", []byte("x"))) }},
		{"duplicate blob", func(v *modelFixture) { v.blobs = append(v.blobs, v.blobs[0]) }},
		{"malformed blob", func(v *modelFixture) { v.blobs[0] = newBeforeBlob(v.blobs[0].key, []byte("wrong")) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := fixture(t, backupjournal.Present, backupjournal.Absent)
			test.change(&value)
			assertInvalid(t, value.manifest, value.blobs, value.before)
		})
	}
}
func TestModelKeepsOrderCopiesAndPrivacy(t *testing.T) {
	value := fixture(t, backupjournal.Present, backupjournal.Present)
	plan := assertPlan(t, value, value.before, atBefore, true)
	for index, want := range []entryKey{"pi\x00config.json", "opencode\x00config.json", "claude-code\x00config.json"} {
		if plan.entries[index].key != want {
			t.Fatalf("entry %d key = %q, want %q", index, plan.entries[index].key, want)
		}
	}
	raw := []byte("copy")
	present, blob := newCurrentPresent("key", 0600, raw), newBeforeBlob("key", raw)
	raw[0], value.blobs[0].bytes[0] = 'X', 'X'
	if string(present.bytes) != "copy" || string(blob.bytes) != "copy" || plan.entries[0].before.bytes[0] == 'X' {
		t.Fatal("model leaked mutable evidence")
	}
	data, err := json.Marshal(plan)
	if err != nil || string(data) != "{}" {
		t.Fatalf("plan exposed private evidence: %s, %v", data, err)
	}
}
func assertPlan(t *testing.T, value modelFixture, current []currentEvidence, want classification, ready bool) recoveryPlan {
	t.Helper()
	plan, err := classify(value.manifest, value.blobs, current)
	if err != nil || len(plan.entries) != 3 || plan.entries[0].state != want || plan.ready != ready {
		t.Fatalf("plan = %#v, err = %v", plan, err)
	}
	return plan
}
func assertInvalid(t *testing.T, manifest backupjournal.Manifest, blobs []beforeBlob, current []currentEvidence) {
	t.Helper()
	if _, err := classify(manifest, blobs, current); err == nil {
		t.Fatal("accepted invalid evidence")
	}
}
func fixture(t *testing.T, before, after backupjournal.Existence, data ...[]byte) modelFixture {
	t.Helper()
	beforeData, afterData := []byte("before"), []byte("after")
	if len(data) != 0 {
		beforeData, afterData = data[0], data[1]
	}
	roots := make([]backupjournal.RootBinding, 0, 3)
	inputs := make([]backupjournal.RecoverableEntryInput, 0, 3)
	for _, runtime := range runtimeList {
		root, err := backupjournal.NewRootBinding(runtime, backupjournal.RootKind(runtime), "/"+string(runtime))
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
		inputs = append(inputs, backupjournal.RecoverableEntryInput{Before: entry(runtime, before, beforeData), After: evidence(after, afterData)})
	}
	manifest, err := backupjournal.NewRecoverable(hash("transaction"), hash("candidate"), roots, inputs)
	if err != nil {
		t.Fatal(err)
	}
	value := modelFixture{manifest: manifest}
	for _, item := range manifest.Entries() {
		key := keyFor(item)
		if item.Existence == backupjournal.Present {
			value.blobs = append(value.blobs, newBeforeBlob(key, beforeData))
			value.before = append(value.before, newCurrentPresent(key, item.Mode, beforeData))
		} else {
			value.before = append(value.before, newCurrentAbsent(key))
		}
		if after := item.AfterEvidence(); after.Existence == backupjournal.Present {
			value.after = append(value.after, newCurrentPresent(key, after.Mode, afterData))
		} else {
			value.after = append(value.after, newCurrentAbsent(key))
		}
	}
	return value
}

var runtimeList = []backupjournal.Runtime{backupjournal.RuntimePi, backupjournal.RuntimeOpenCode, backupjournal.RuntimeClaude}

func entry(runtime backupjournal.Runtime, existence backupjournal.Existence, data []byte) backupjournal.EntryInput {
	mode, hash, length := metadata(existence, data)
	return backupjournal.EntryInput{Runtime: runtime, Root: backupjournal.RootKind(runtime), RelativePath: "config.json", Existence: existence, Mode: mode, SHA256: hash, Length: length}
}
func evidence(existence backupjournal.Existence, data []byte) backupjournal.Evidence {
	mode, hash, length := metadata(existence, data)
	return backupjournal.Evidence{Existence: existence, Mode: mode, SHA256: hash, Length: length}
}
func metadata(existence backupjournal.Existence, data []byte) (uint32, string, int64) {
	if existence == backupjournal.Absent {
		return 0, "", 0
	}
	return 0600, hashBytes(data), int64(len(data))
}
func hash(value string) string { return hashBytes([]byte(value)) }
