package qaactor

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

func TestBind(t *testing.T) {
	projection := canonicalProjection(t)

	binding, err := Bind(projection)
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if binding.Contract() != ActorArtifactContract ||
		binding.Runtime() != runtimematrix.RuntimePi ||
		binding.CatalogFingerprint() != projection.Actors()[0].CatalogFingerprint() {
		t.Fatalf("Bind() returned unexpected binding identity")
	}

	actors := binding.Actors()
	projected := projection.Actors()
	contracts := qarole.Catalog()
	if len(actors) != len(contracts) {
		t.Fatalf("Bind() returned %d actors, want %d", len(actors), len(contracts))
	}
	for index, contract := range contracts {
		actor := actors[index]
		want := projected[index]
		if actor.RoleID() != contract.ID ||
			actor.RoleContractVersion() != want.RoleContractVersion() ||
			actor.LogicalID() != want.LogicalID() ||
			actor.Name() != want.Name() ||
			actor.ActorContract() != want.ActorContract() ||
			actor.CatalogFingerprint() != want.CatalogFingerprint() ||
			actor.SourceSHA256() != want.SourceSHA256() ||
			actor.GeneratedSHA256() != want.GeneratedSHA256() ||
			string(actor.Content()) != string(want.Content()) {
			t.Fatalf("Bind() actor %q differs from projection", contract.ID)
		}
	}
	if want := bindingDigest(projection); binding.BindingSHA256() != want {
		t.Fatalf("Bind() SHA256 = %q, want %q", binding.BindingSHA256(), want)
	}
}

func TestBindRejectsMalformedProjection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Projection)
	}{
		{"zero", func(projection *Projection) { projection.actors = nil }},
		{"short", func(projection *Projection) { projection.actors = projection.actors[:5] }},
		{"extra", func(projection *Projection) { projection.actors = append(projection.actors, projection.actors[0]) }},
		{"reordered", func(projection *Projection) {
			projection.actors[0], projection.actors[1] = projection.actors[1], projection.actors[0]
		}},
		{"duplicate role", func(projection *Projection) { projection.actors[1].roleID = projection.actors[0].roleID }},
		{"role contract", func(projection *Projection) { projection.actors[0].roleContractVersion = "other" }},
		{"logical ID", func(projection *Projection) { projection.actors[0].logicalID = "actors/other" }},
		{"name", func(projection *Projection) { projection.actors[0].name = "cortex-other" }},
		{"actor contract", func(projection *Projection) { projection.actors[0].actorContract = "other" }},
		{"catalog fingerprint", func(projection *Projection) { projection.actors[1].catalogFingerprint = strings.Repeat("a", 64) }},
		{"invalid catalog fingerprint", func(projection *Projection) { projection.actors[0].catalogFingerprint = "invalid" }},
		{"source hash", func(projection *Projection) { projection.actors[0].sourceSHA256 = "invalid" }},
		{"generated hash", func(projection *Projection) { projection.actors[0].generatedSHA256 = "invalid" }},
		{"empty content", func(projection *Projection) { projection.actors[0].content = nil }},
		{"content hash mismatch", func(projection *Projection) { projection.actors[0].content = append(projection.actors[0].content, 'x') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projection := canonicalProjection(t)
			tc.mutate(&projection)
			binding, err := Bind(projection)
			if err == nil || binding.Contract() != "" || len(binding.Actors()) != 0 {
				t.Fatalf("Bind() = (%#v, %v), want rejected projection", binding, err)
			}
		})
	}
}

func TestBindIsDeterministicAndDetached(t *testing.T) {
	projection := canonicalProjection(t)
	first, err := Bind(projection)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bind(projection)
	if err != nil || first.BindingSHA256() != second.BindingSHA256() {
		t.Fatalf("Bind() deterministic result = (%q, %q, %v)", first.BindingSHA256(), second.BindingSHA256(), err)
	}

	actors := first.Actors()
	content := actors[0].Content()
	actors[0] = ProjectedActor{}
	content[0] = 'X'
	projection.actors[0].content[0] = 'Y'
	fresh := first.Actors()
	if fresh[0].RoleID() != qarole.RequirementsAnalyst || fresh[0].Content()[0] == 'X' || fresh[0].Content()[0] == 'Y' {
		t.Fatal("Bind() exposed shared actor values or content")
	}
}

func TestBindAcceptsInternallyValidChangedContent(t *testing.T) {
	projection := canonicalProjection(t)
	original, err := Bind(projection)
	if err != nil {
		t.Fatal(err)
	}
	projection.actors[0].content = append(projection.actors[0].content, 'x')
	projection.actors[0].generatedSHA256 = digest(projection.actors[0].content)

	changed, err := Bind(projection)
	if err != nil || changed.BindingSHA256() == original.BindingSHA256() {
		t.Fatalf("Bind() changed content = (%#v, %v)", changed, err)
	}
}

func TestBindDoesNotRecomputeProjectionProvenance(t *testing.T) {
	projection := canonicalProjection(t)
	for index := range projection.actors {
		projection.actors[index].catalogFingerprint = strings.Repeat("b", 64)
		projection.actors[index].sourceSHA256 = strings.Repeat("c", 64)
	}

	binding, err := Bind(projection)
	if err != nil || binding.CatalogFingerprint() != strings.Repeat("b", 64) {
		t.Fatalf("Bind() provenance-only change = (%#v, %v)", binding, err)
	}
}

func canonicalProjection(t *testing.T) Projection {
	t.Helper()
	projection, err := ProjectPi(canonicalSet(t))
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func bindingDigest(projection Projection) string {
	actors := projection.Actors()
	frames := [][]byte{
		[]byte(ActorArtifactContract),
		[]byte(actors[0].CatalogFingerprint()),
		[]byte(actors[0].RoleContractVersion()),
		[]byte(actors[0].ActorContract()),
	}
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(actors)))
	frames = append(frames, count[:])
	for _, actor := range actors {
		frames = append(frames,
			[]byte(actor.RoleID()),
			[]byte(actor.SourceSHA256()),
			[]byte(actor.GeneratedSHA256()),
			[]byte("agents/"+actor.Name()+".md"),
		)
	}
	hash := sha256.New()
	for _, frame := range frames {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(frame)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(frame)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func digest(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

func TestBindHasNoRuntimeInput(t *testing.T) {
	typeOfBind := reflect.TypeOf(Bind)
	if typeOfBind.NumIn() != 1 || typeOfBind.In(0) != reflect.TypeOf(Projection{}) ||
		typeOfBind.NumOut() != 2 || typeOfBind.Out(0) != reflect.TypeOf(Binding{}) {
		t.Fatal("Bind must accept one Projection and return one Binding with an error")
	}
}
