package qaactor

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestProjectPi(t *testing.T) {
	set := canonicalSet(t)

	projection, err := ProjectPi(set)
	if err != nil {
		t.Fatalf("ProjectPi() error = %v", err)
	}

	actors := projection.Actors()
	sources := set.Sources()
	rendered := set.Actors()
	contracts := qarole.Catalog()
	if len(actors) != len(contracts) {
		t.Fatalf("projected actor count = %d, want %d", len(actors), len(contracts))
	}

	for index, contract := range contracts {
		actor := actors[index]
		if actor.RoleID() != contract.ID ||
			actor.LogicalID() != "actors/"+string(contract.ID) ||
			actor.Name() != "cortex-"+string(contract.ID) ||
			actor.RoleContractVersion() != contract.ContractVersion ||
			actor.ActorContract() != ActorContractVersion ||
			actor.CatalogFingerprint() != set.CatalogFingerprint() ||
			actor.SourceSHA256() != sources[index].SourceSHA256() ||
			actor.GeneratedSHA256() != rendered[index].GeneratedSHA256() ||
			string(actor.Content()) != string(rendered[index].Content()) {
			t.Fatalf("projected actor %q differs from its validated source", contract.ID)
		}
		if actor.GeneratedSHA256() != fmt.Sprintf("%x", sha256.Sum256(actor.Content())) {
			t.Fatalf("projected actor %q has an invalid content hash", contract.ID)
		}
	}
}

func TestProjectPiRejectsInvalidSets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Set)
	}{
		{"wrong actor contract", func(set *Set) { set.actorContract = "other" }},
		{"catalog fingerprint drift", func(set *Set) { set.catalogFingerprint = "invalid" }},
		{"reordered actors", func(set *Set) { set.actors[0], set.actors[1] = set.actors[1], set.actors[0] }},
		{"duplicate role", func(set *Set) { set.actors[1].roleID = set.actors[0].roleID }},
		{"role contract drift", func(set *Set) { set.actors[0].roleContractVersion = "other" }},
		{"source hash drift", func(set *Set) { set.actors[0].sourceSHA256 = "invalid" }},
		{"content hash drift", func(set *Set) { set.actors[0].generatedSHA256 = "invalid" }},
		{"content drift", func(set *Set) { set.actors[0].content = append(set.actors[0].content, 'X') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := canonicalSet(t)
			tc.mutate(&set)
			projection, err := ProjectPi(set)
			if err == nil {
				t.Fatal("ProjectPi() accepted an invalid set")
			}
			if len(projection.Actors()) != 0 {
				t.Fatal("ProjectPi() returned actors for an invalid set")
			}
		})
	}
}

func TestProjectPiHasNoRuntimeSelection(t *testing.T) {
	typeOfProjectPi := reflect.TypeOf(ProjectPi)
	if typeOfProjectPi.NumIn() != 1 || typeOfProjectPi.In(0) != reflect.TypeOf(Set{}) {
		t.Fatal("ProjectPi must accept only one validated Set")
	}
	if typeOfProjectPi.NumOut() != 2 || typeOfProjectPi.Out(0) != reflect.TypeOf(Projection{}) {
		t.Fatal("ProjectPi must return only a Pi projection and an error")
	}
}

func TestProjectPiReturnsDetachedValues(t *testing.T) {
	set := canonicalSet(t)
	projection, err := ProjectPi(set)
	if err != nil {
		t.Fatal(err)
	}

	actors := projection.Actors()
	content := actors[0].Content()
	actors[0] = ProjectedActor{}
	content[0] = 'X'
	set.actors[0].content[0] = 'Y'

	fresh := projection.Actors()
	if fresh[0].RoleID() != qarole.RequirementsAnalyst || fresh[0].Content()[0] == 'X' || fresh[0].Content()[0] == 'Y' {
		t.Fatal("ProjectPi() returned shared actor values or content")
	}
}

func canonicalSet(t *testing.T) Set {
	t.Helper()
	_, set := testRendered(t)
	return set
}
