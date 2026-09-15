package installobserve

import "testing"

func TestMatchesAbsentRootStructure(t *testing.T) {
	valid := FilesystemObservation{rootAbsent: true, exact: map[string]ExactFile{}, slots: []SlotObservation{{LogicalID: "skill/example"}}}
	for _, tt := range []struct {
		name        string
		observation FilesystemObservation
		want        bool
	}{
		{"empty exact evidence", valid, true},
		{"nil exact evidence", FilesystemObservation{rootAbsent: true, slots: valid.slots}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesAbsentRootStructure(tt.observation, []string{"skill/example"}); got != tt.want {
				t.Errorf("matchesAbsentRootStructure() = %t, want %t", got, tt.want)
			}
		})
	}
}
