// Package backupjournal defines path-private durable backup journal metadata.
package backupjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const SchemaVersion = 1
const recoverableSchemaVersion = 2
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

// Evidence describes an image without its bytes or a filesystem path.
type Evidence struct {
	Existence Existence `json:"existence"`
	Mode      uint32    `json:"mode"`
	SHA256    string    `json:"sha256"`
	Length    int64     `json:"length"`
}

// RecoverableEntryInput describes both before and intended-after evidence.
type RecoverableEntryInput struct {
	Before EntryInput
	After  Evidence
}

// RootBinding binds a runtime root without retaining its path.
type RootBinding struct {
	runtime Runtime
	kind    RootKind
	digest  string
}

func (binding RootBinding) Runtime() Runtime { return binding.runtime }
func (binding RootBinding) Kind() RootKind   { return binding.kind }
func (binding RootBinding) Digest() string   { return binding.digest }

// NewRootBinding constructs an opaque binding for a clean absolute root path.
func NewRootBinding(runtime Runtime, kind RootKind, root string) (RootBinding, error) {
	if runtimeRank(runtime) < 0 || !matchesRoot(runtime, kind) || !cleanAbsolute(root) {
		return RootBinding{}, errors.New("backup journal: invalid root binding")
	}
	return RootBinding{runtime, kind, rootDigest(runtime, kind, root)}, nil
}

// MatchesRoot reports whether path normalizes to this binding's root.
func (binding RootBinding) MatchesRoot(root string) bool {
	clean, ok := normalizedAbsolute(root)
	return ok && validBinding(binding) && binding.digest == rootDigest(binding.runtime, binding.kind, clean)
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
	after        Evidence
}

func (entry Entry) AfterEvidence() Evidence { return entry.after }

// Manifest is an immutable journal identity and its canonical entries.
type Manifest struct {
	schemaVersion        int
	transactionID        string
	candidateFingerprint string
	state                State
	roots                []RootBinding
	entries              []Entry
}

func (manifest Manifest) Version() int {
	if manifest.schemaVersion == recoverableSchemaVersion {
		return recoverableSchemaVersion
	}
	return SchemaVersion
}
func (manifest Manifest) Recoverable() bool            { return manifest.Version() == recoverableSchemaVersion }
func (manifest Manifest) TransactionID() string        { return manifest.transactionID }
func (manifest Manifest) CandidateFingerprint() string { return manifest.candidateFingerprint }
func (manifest Manifest) State() State                 { return manifest.state }
func (manifest Manifest) RootBindings() []RootBinding {
	return append([]RootBinding(nil), manifest.roots...)
}
func (manifest Manifest) Entries() []Entry { return append([]Entry(nil), manifest.entries...) }

// New constructs a schema-v1 prepared journal for all supported runtime roots.
func New(transactionID, candidateFingerprint string, inputs []EntryInput) (Manifest, error) {
	if !validHash(transactionID) || !validHash(candidateFingerprint) || len(inputs) == 0 || len(inputs) > maxEntries {
		return Manifest{}, errors.New("backup journal: invalid identity or entries")
	}
	entries, err := entriesFor(inputs)
	if err != nil {
		return Manifest{}, err
	}
	sort.Slice(entries, func(left, right int) bool { return entryLess(entries[left], entries[right]) })
	return Manifest{SchemaVersion, transactionID, candidateFingerprint, Prepared, nil, entries}, nil
}

// NewRecoverable constructs a schema-v2 journal with intended-after evidence.
func NewRecoverable(transactionID, candidateFingerprint string, roots []RootBinding, inputs []RecoverableEntryInput) (Manifest, error) {
	if !validHash(transactionID) || !validHash(candidateFingerprint) || len(inputs) == 0 || len(inputs) > maxEntries || !canonicalRoots(roots) {
		return Manifest{}, errors.New("backup journal: invalid recoverable journal")
	}
	before := make([]EntryInput, len(inputs))
	entries := make([]Entry, len(inputs))
	for index, input := range inputs {
		before[index] = input.Before
		if !validEvidence(input.After) || sameEvidence(input.Before, input.After) {
			return Manifest{}, errors.New("backup journal: invalid recoverable entry")
		}
	}
	base, err := entriesFor(before)
	if err != nil || !canonicalEntries(base) {
		return Manifest{}, errors.New("backup journal: invalid recoverable entry")
	}
	for index := range base {
		if !hasBinding(roots, base[index].Runtime, base[index].Root) {
			return Manifest{}, errors.New("backup journal: invalid recoverable binding")
		}
		base[index].after = inputs[index].After
		entries[index] = base[index]
	}
	return Manifest{recoverableSchemaVersion, transactionID, candidateFingerprint, Prepared, append([]RootBinding(nil), roots...), entries}, nil
}

// Transition returns the sole permitted terminal transition from prepared.
func (manifest Manifest) Transition(next State) (Manifest, error) {
	if manifest.state != Prepared || (next != Committed && next != Recovered) {
		return Manifest{}, errors.New("backup journal: invalid transition")
	}
	return Manifest{manifest.Version(), manifest.transactionID, manifest.candidateFingerprint, next, manifest.RootBindings(), manifest.Entries()}, nil
}

type wire struct {
	SchemaVersion        int     `json:"schemaVersion"`
	TransactionID        string  `json:"transactionID"`
	CandidateFingerprint string  `json:"candidateFingerprint"`
	State                State   `json:"state"`
	Entries              []Entry `json:"entries"`
}
type rootWire struct {
	Runtime Runtime  `json:"runtime"`
	Root    RootKind `json:"root"`
	Digest  string   `json:"digest"`
}
type entryWire struct {
	Runtime      Runtime   `json:"runtime"`
	Root         RootKind  `json:"root"`
	RelativePath string    `json:"relativePath"`
	Existence    Existence `json:"existence"`
	Mode         uint32    `json:"mode"`
	SHA256       string    `json:"sha256"`
	Length       int64     `json:"length"`
	BlobName     string    `json:"blobName"`
	After        Evidence  `json:"after"`
}
type wireV2 struct {
	SchemaVersion        int         `json:"schemaVersion"`
	TransactionID        string      `json:"transactionID"`
	CandidateFingerprint string      `json:"candidateFingerprint"`
	State                State       `json:"state"`
	Roots                []rootWire  `json:"roots"`
	Entries              []entryWire `json:"entries"`
}

// MarshalJSON emits the canonical representation for this manifest version.
func (manifest Manifest) MarshalJSON() ([]byte, error) {
	if !manifest.Recoverable() {
		return json.Marshal(wire{SchemaVersion, manifest.transactionID, manifest.candidateFingerprint, manifest.state, manifest.Entries()})
	}
	roots := make([]rootWire, len(manifest.roots))
	for index, root := range manifest.roots {
		roots[index] = rootWire{root.runtime, root.kind, root.digest}
	}
	entries := make([]entryWire, len(manifest.entries))
	for index, entry := range manifest.entries {
		entries[index] = entryWire{entry.Runtime, entry.Root, entry.RelativePath, entry.Existence, entry.Mode, entry.SHA256, entry.Length, entry.BlobName, entry.after}
	}
	return json.Marshal(wireV2{recoverableSchemaVersion, manifest.transactionID, manifest.candidateFingerprint, manifest.state, roots, entries})
}

// Parse accepts only canonical schema-v1 or schema-v2 journal JSON.
func Parse(data []byte) (Manifest, error) {
	var version struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if decodeVersion(data, &version) != nil {
		return Manifest{}, errors.New("backup journal: invalid JSON")
	}
	switch version.SchemaVersion {
	case SchemaVersion:
		var stored wire
		if decode(data, &stored) != nil || !validState(stored.State) {
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
		return parsedCanonical(data, manifest, stored.State)
	case recoverableSchemaVersion:
		var stored wireV2
		if decode(data, &stored) != nil || !validState(stored.State) {
			return Manifest{}, errors.New("backup journal: invalid recoverable state")
		}
		roots := make([]RootBinding, len(stored.Roots))
		for index, root := range stored.Roots {
			roots[index] = RootBinding{root.Runtime, root.Root, root.Digest}
		}
		inputs := make([]RecoverableEntryInput, len(stored.Entries))
		for index, entry := range stored.Entries {
			if entry.BlobName != expectedBlob(Entry{Runtime: entry.Runtime, Root: entry.Root, RelativePath: entry.RelativePath, Existence: entry.Existence}) {
				return Manifest{}, errors.New("backup journal: invalid recoverable blob")
			}
			inputs[index] = RecoverableEntryInput{EntryInput{entry.Runtime, entry.Root, entry.RelativePath, entry.Existence, entry.Mode, entry.SHA256, entry.Length}, entry.After}
		}
		manifest, err := NewRecoverable(stored.TransactionID, stored.CandidateFingerprint, roots, inputs)
		if err != nil {
			return Manifest{}, err
		}
		return parsedCanonical(data, manifest, stored.State)
	default:
		return Manifest{}, errors.New("backup journal: unknown schema version")
	}
}

func parsedCanonical(data []byte, manifest Manifest, state State) (Manifest, error) {
	var err error
	if state != Prepared {
		manifest, err = manifest.Transition(state)
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
func decode(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func decodeVersion(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func validState(state State) bool {
	return state == Prepared || state == Committed || state == Recovered
}
func entriesFor(inputs []EntryInput) ([]Entry, error) {
	entries := make([]Entry, len(inputs))
	destinations, runtimes := map[Runtime][]string{}, map[Runtime]bool{}
	for index, input := range inputs {
		if !validInput(input) {
			return nil, errors.New("backup journal: invalid entry")
		}
		for _, existing := range destinations[input.Runtime] {
			if overlaps(existing, input.RelativePath) {
				return nil, errors.New("backup journal: overlapping destination")
			}
		}
		destinations[input.Runtime] = append(destinations[input.Runtime], input.RelativePath)
		runtimes[input.Runtime] = true
		entry := Entry{Runtime: input.Runtime, Root: input.Root, RelativePath: input.RelativePath, Existence: input.Existence, Mode: input.Mode, SHA256: input.SHA256, Length: input.Length}
		if input.Existence == Present {
			entry.BlobName = blobName(input.Runtime, input.RelativePath)
		}
		entries[index] = entry
	}
	if !runtimes[RuntimePi] || !runtimes[RuntimeOpenCode] || !runtimes[RuntimeClaude] {
		return nil, errors.New("backup journal: every runtime is required")
	}
	return entries, nil
}
func validInput(entry EntryInput) bool {
	return runtimeRank(entry.Runtime) >= 0 && matchesRoot(entry.Runtime, entry.Root) && relative(entry.RelativePath) && validEvidence(Evidence{entry.Existence, entry.Mode, entry.SHA256, entry.Length})
}
func validEvidence(evidence Evidence) bool {
	if evidence.Existence == Absent {
		return evidence.Mode == 0 && evidence.SHA256 == "" && evidence.Length == 0
	}
	return evidence.Existence == Present && (evidence.Mode == 0600 || evidence.Mode == 0644) && validHash(evidence.SHA256) && evidence.Length >= 0 && evidence.Length <= maxLength
}
func sameEvidence(before EntryInput, after Evidence) bool {
	return before.Existence == after.Existence && before.Mode == after.Mode && before.SHA256 == after.SHA256 && before.Length == after.Length
}
func canonicalRoots(roots []RootBinding) bool {
	if len(roots) != 3 {
		return false
	}
	for index, root := range roots {
		if !validBinding(root) || runtimeRank(root.runtime) != index {
			return false
		}
	}
	return true
}
func validBinding(binding RootBinding) bool {
	return runtimeRank(binding.runtime) >= 0 && matchesRoot(binding.runtime, binding.kind) && validHash(binding.digest)
}
func hasBinding(roots []RootBinding, runtime Runtime, kind RootKind) bool {
	for _, root := range roots {
		if root.runtime == runtime && root.kind == kind {
			return true
		}
	}
	return false
}
func canonicalEntries(entries []Entry) bool {
	for index := 1; index < len(entries); index++ {
		if !entryLess(entries[index-1], entries[index]) {
			return false
		}
	}
	return true
}
func entryLess(left, right Entry) bool {
	if left.Runtime != right.Runtime {
		return runtimeRank(left.Runtime) < runtimeRank(right.Runtime)
	}
	return left.RelativePath < right.RelativePath
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
func rootDigest(runtime Runtime, kind RootKind, root string) string {
	digest := sha256.Sum256([]byte(string(runtime) + "\x00" + string(kind) + "\x00" + root))
	return hex.EncodeToString(digest[:])
}
func cleanAbsolute(value string) bool {
	clean, ok := normalizedAbsolute(value)
	return ok && clean == value
}
func normalizedAbsolute(value string) (string, bool) {
	if !filepath.IsAbs(value) {
		return "", false
	}
	return filepath.Clean(value), true
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
