package qaroute

import (
	"reflect"
	"testing"
)

func TestBackendsListsAdmittedSetInStableOrder(t *testing.T) {
	got := Backends()
	if !reflect.DeepEqual(got, []string{"pi", "claude", "opencode"}) {
		t.Fatalf("Backends() = %v", got)
	}
	for _, backend := range got {
		if !Admits(backend) {
			t.Fatalf("%s not admitted", backend)
		}
	}
	got[0] = "mutated"
	if Backends()[0] != "pi" {
		t.Fatal("Backends shares its backing array")
	}
}
