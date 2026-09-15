package qaactor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

func TestPlanDestinations(t *testing.T) {
	binding := canonicalBinding(t)

	plan, err := PlanDestinations(binding)
	if err != nil {
		t.Fatalf("PlanDestinations() error = %v", err)
	}

	destinations := plan.Destinations()
	contracts := qarole.Catalog()
	if len(destinations) != len(contracts) {
		t.Fatalf("PlanDestinations() returned %d destinations, want %d", len(destinations), len(contracts))
	}
	for index, contract := range contracts {
		destination := destinations[index]
		actor := binding.Actors()[index]
		if destination.LogicalID() != "actors/"+string(contract.ID) ||
			destination.Kind() != DestinationKindPiActor ||
			destination.RoleID() != contract.ID ||
			destination.RelativePath() != "agents/cortex-"+string(contract.ID)+".md" ||
			destination.ActorContract() != ActorContractVersion ||
			destination.SHA256() != actor.GeneratedSHA256() ||
			string(destination.Content()) != string(actor.Content()) {
			t.Fatalf("destination %q differs from its binding", contract.ID)
		}
		if !isSHA256Of(destination.SHA256(), destination.Content()) {
			t.Fatalf("destination %q has an invalid content hash", contract.ID)
		}
	}
}

func TestPlanDestinationsReturnsDetachedValues(t *testing.T) {
	binding := canonicalBinding(t)
	plan, err := PlanDestinations(binding)
	if err != nil {
		t.Fatal(err)
	}

	destinations := plan.Destinations()
	content := destinations[0].Content()
	destinations[0] = Destination{}
	content[0] = 'X'
	binding.actors[0].content[0] = 'Y'

	fresh := plan.Destinations()
	if fresh[0].RoleID() != qarole.RequirementsAnalyst || fresh[0].Content()[0] == 'X' || fresh[0].Content()[0] == 'Y' {
		t.Fatal("PlanDestinations() exposed shared destination values or content")
	}
}

func TestPlanDestinationsRejectsMalformedBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Binding)
	}{
		{"zero actors", func(binding *Binding) { binding.actors = nil }},
		{"short actors", func(binding *Binding) { binding.actors = binding.actors[:5] }},
		{"artifact contract", func(binding *Binding) { binding.contract = "other" }},
		{"alternate runtime", func(binding *Binding) { binding.runtime = runtimematrix.RuntimeOpenCode }},
		{"invalid catalog fingerprint", func(binding *Binding) { binding.catalogFingerprint = "invalid" }},
		{"catalog fingerprint mismatch", func(binding *Binding) { binding.actors[1].catalogFingerprint = strings.Repeat("a", 64) }},
		{"unknown role", func(binding *Binding) { binding.actors[0].roleID = "other" }},
		{"noncanonical logical ID", func(binding *Binding) { binding.actors[0].logicalID = "actors/../other" }},
		{"content hash mismatch", func(binding *Binding) { binding.actors[0].content = append(binding.actors[0].content, 'X') }},
		{"reordered actors", func(binding *Binding) {
			binding.actors[0], binding.actors[1] = binding.actors[1], binding.actors[0]
		}},
		{"altered binding digest", func(binding *Binding) { binding.bindingSHA256 = strings.Repeat("a", 64) }},
		{"uppercase binding digest", func(binding *Binding) { binding.bindingSHA256 = strings.ToUpper(binding.bindingSHA256) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := canonicalBinding(t)
			tc.mutate(&binding)
			plan, err := PlanDestinations(binding)
			if err == nil || len(plan.Destinations()) != 0 {
				t.Fatalf("PlanDestinations() = (%#v, %v), want rejected binding", plan, err)
			}
		})
	}
}

func TestPlanDestinationsRejectsBindingFingerprintMismatch(t *testing.T) {
	binding := canonicalBinding(t)
	binding.catalogFingerprint = strings.Repeat("b", 64)
	binding.bindingSHA256 = bindingSHA256(binding)

	plan, err := PlanDestinations(binding)
	if err == nil || len(plan.Destinations()) != 0 {
		t.Fatalf("PlanDestinations() = (%#v, %v), want rejected binding fingerprint mismatch", plan, err)
	}
}

func TestPlanDestinationsHasClosedAPI(t *testing.T) {
	typeOfPlanDestinations := reflect.TypeOf(PlanDestinations)
	if typeOfPlanDestinations.NumIn() != 1 || typeOfPlanDestinations.In(0) != reflect.TypeOf(Binding{}) ||
		typeOfPlanDestinations.NumOut() != 2 || typeOfPlanDestinations.Out(0) != reflect.TypeOf(Plan{}) {
		t.Fatal("PlanDestinations must accept only Binding and return only Plan with an error")
	}

	planType := reflect.TypeOf(Plan{})
	for index := range planType.NumField() {
		if planType.Field(index).IsExported() {
			t.Fatal("Plan must not expose mutable fields")
		}
	}

	destinationType := reflect.TypeOf(Plan{}.Destinations()).Elem()
	if destinationType != reflect.TypeOf(Destination{}) {
		t.Fatal("Plan destinations must use the exported Destination value type")
	}
	for index := range destinationType.NumField() {
		if destinationType.Field(index).IsExported() {
			t.Fatal("Destination must not expose mutable fields")
		}
	}
}

func canonicalBinding(t *testing.T) Binding {
	t.Helper()
	binding, err := Bind(canonicalProjection(t))
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
