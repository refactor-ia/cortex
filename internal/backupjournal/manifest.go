// Package backupjournal defines path-private durable backup journal metadata.
package backupjournal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
)

const SchemaVersion = 1
const maxEntries = 128
const maxLength int64 = 32 << 20

type Runtime string
type RootKind string
type State string
type Existence string

const (
	RuntimePi       Runtime   = "pi"
	RuntimeOpenCode Runtime   = "opencode"
	RuntimeClaude   Runtime   = "claude-code"
	RootPi          RootKind  = "pi"
	RootOpenCode    RootKind  = "opencode"
	RootClaude      RootKind  = "claude-code"
	Prepared        State     = "prepared"
	Committed       State     = "committed"
	Recovered       State     = "recovered"
	Absent          Existence = "absent"
	Present         Existence = "present"
)

// EntryInput describes a before-image without its bytes or a filesystem path.
type EntryInput struct {
	Runtime      Runtime
	Root         RootKind
	RelativePath string
	Existence    Existence
	Mode         uint32
	SHA256       string
	Length       int64
}

// Entry exposes immutable path-private before-image metadata.
type Entry struct {
	Runtime      Runtime   `json:"runtime"`
	Root         RootKind  `json:"root"`
	RelativePath string    `json:"relativePath"`
	Existence    Existence `json:"existence"`
	Mode         uint32    `json:"mode"`
	SHA256       string    `json:"sha256"`
	Length       int64     `json:"length"`
	BlobName     string    `json:"blobName"`
}

// Manifest is an immutable journal identity and its canonical entries.
type Manifest struct {
	transactionID        string
	candidateFingerprint string
	state                State
	entries              []Entry
}

func (manifest Manifest) TransactionID() string        { return manifest.transactionID }
func (manifest Manifest) CandidateFingerprint() string { return manifest.candidateFingerprint }
func (manifest Manifest) State() State                 { return manifest.state }
func (manifest Manifest) Entries() []Entry             { return append([]Entry(nil), manifest.entries...) }

// New constructs a prepared journal for all supported runtime roots.
func New(transactionID, candidateFingerprint string, inputs []EntryInput) (Manifest, error) {
	if !validHash(transactionID) || !validHash(candidateFingerprint) || len(inputs) == 0 || len(inputs) > maxEntries {
		return Manifest{}, errors.New("backup journal: invalid identity or entries")
	}
	entries := make([]Entry, len(inputs))
	destinations, runtimes := map[Runtime][]string{}, map[Runtime]bool{}
	for index, input := range inputs {
		if !validInput(input) {
			return Manifest{}, errors.New("backup journal: invalid entry")
		}
		for _, existing := range destinations[input.Runtime] {
			if overlaps(existing, input.RelativePath) {
				return Manifest{}, errors.New("backup journal: overlapping destination")
			}
		}
		destinations[input.Runtime] = append(destinations[input.Runtime], input.RelativePath)
		runtimes[input.Runtime] = true
		entry := Entry{input.Runtime, input.Root, input.RelativePath, input.Existence, input.Mode, input.SHA256, input.Length, ""}
		if input.Existence == Present {
			entry.BlobName = blobName(input.Runtime, input.RelativePath)
		}
		entries[index] = entry
	}
	if !runtimes[RuntimePi] || !runtimes[RuntimeOpenCode] || !runtimes[RuntimeClaude] {
		return Manifest{}, errors.New("backup journal: every runtime is required")
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Runtime != entries[right].Runtime {
			return runtimeRank(entries[left].Runtime) < runtimeRank(entries[right].Runtime)
		}
		return entries[left].RelativePath < entries[right].RelativePath
	})
	return Manifest{transactionID, candidateFingerprint, Prepared, entries}, nil
}

// Transition returns the sole permitted terminal transition from prepared.
func (manifest Manifest) Transition(next State) (Manifest, error) {
	if manifest.state != Prepared || (next != Committed && next != Recovered) {
		return Manifest{}, errors.New("backup journal: invalid transition")
	}
	return Manifest{manifest.transactionID, manifest.candidateFingerprint, next, append([]Entry(nil), manifest.entries...)}, nil
}

type wire struct {
	SchemaVersion        int     `json:"schemaVersion"`
	TransactionID        string  `json:"transactionID"`
	CandidateFingerprint string  `json:"candidateFingerprint"`
	State                State   `json:"state"`
	Entries              []Entry `json:"entries"`
}

// MarshalJSON emits the one canonical schema-version-one representation.
func (manifest Manifest) MarshalJSON() ([]byte, error) {
	return json.Marshal(wire{SchemaVersion, manifest.transactionID, manifest.candidateFingerprint, manifest.state, manifest.Entries()})
}

// Parse accepts only canonical schema-version-one journal JSON.
func Parse(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored wire
	if decoder.Decode(&stored) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Manifest{}, errors.New("backup journal: invalid JSON")
	}
	if stored.SchemaVersion != SchemaVersion || (stored.State != Prepared && stored.State != Committed && stored.State != Recovered) {
		return Manifest{}, errors.New("backup journal: invalid stored state")
	}
	inputs := make([]EntryInput, len(stored.Entries))
	for index, entry := range stored.Entries {
		if entry.BlobName != expectedBlob(entry) {
			return Manifest{}, errors.New("backup journal: invalid stored blob")
		}
		inputs[index] = EntryInput{entry.Runtime, entry.Root, entry.RelativePath, entry.Existence, entry.Mode, entry.SHA256, entry.Length}
	}
	manifest, err := New(stored.TransactionID, stored.CandidateFingerprint, inputs)
	if err != nil {
		return Manifest{}, err
	}
	if stored.State != Prepared {
		manifest, err = manifest.Transition(stored.State)
		if err != nil {
			return Manifest{}, err
		}
	}
	canonical, _ := json.Marshal(manifest)
	if !bytes.Equal(data, canonical) {
		return Manifest{}, errors.New("backup journal: noncanonical JSON")
	}
	return manifest, nil
}

func validInput(entry EntryInput) bool {
	if runtimeRank(entry.Runtime) < 0 || !matchesRoot(entry.Runtime, entry.Root) || !relative(entry.RelativePath) {
		return false
	}
	if entry.Existence == Absent {
		return entry.Mode == 0 && entry.SHA256 == "" && entry.Length == 0
	}
	return entry.Existence == Present && (entry.Mode == 0600 || entry.Mode == 0644) && validHash(entry.SHA256) && entry.Length >= 0 && entry.Length <= maxLength
}
func runtimeRank(runtime Runtime) int {
	switch runtime {
	case RuntimePi:
		return 0
	case RuntimeOpenCode:
		return 1
	case RuntimeClaude:
		return 2
	}
	return -1
}
func matchesRoot(runtime Runtime, root RootKind) bool { return RootKind(runtime) == root }
func blobName(runtime Runtime, relativePath string) string {
	return "blobs/" + string(runtime) + "/" + relativePath
}
func expectedBlob(entry Entry) string {
	if entry.Existence == Absent {
		return ""
	}
	return blobName(entry.Runtime, entry.RelativePath)
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
func overlaps(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
func relative(value string) bool {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
