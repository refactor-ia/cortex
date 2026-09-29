package qapi

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func TestEvidenceValidation(t *testing.T) {
	const valid = `[{"id":"test-1","command":"go test -run Selected","exit_code":0,"output_tail":"no tests to run","provenance":"caller fixture","truncated":true}]`
	records := make([]string, 9)
	for i := range records {
		records[i] = `{"id":"test-` + strconv.Itoa(i) + `"}`
	}
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"valid zero-test observation", valid, true},
		{"missing fields", `[{"id":"unknown"}]`, true},
		{"explicit unknowns", `[{"id":"unknown","exit_code":null,"truncated":null}]`, true},
		{"contradictory observations remain supplied", `[{"id":"conflict","exit_code":0,"output_tail":"FAIL selected test"}]`, true},
		{"explicit failure and complete capture", `[{"id":"failure","exit_code":1,"truncated":false}]`, true},
		{"empty collection", `[]`, true},
		{"eight records", `[` + strings.Join(records[:8], ",") + `]`, true},
		{"nine records", `[` + strings.Join(records, ",") + `]`, false},
		{"empty file", "", false},
		{"null collection", `null`, false},
		{"wrong collection type", `{}`, false},
		{"malformed JSON", `[{`, false},
		{"trailing JSON", valid + `[]`, false},
		{"invalid UTF8", valid + "\xff", false},
		{"missing ID", `[{}]`, false},
		{"blank ID", `[{"id":" "}]`, false},
		{"ID control character", `[{"id":"a\nb"}]`, false},
		{"duplicate IDs", `[{"id":"x"},{"id":"x"}]`, false},
		{"duplicate fields", `[{"id":"x","exit_code":1,"exit_code":0}]`, false},
		{"unknown field", `[{"id":"x","passed":true}]`, false},
		{"case alias cannot override observations", `[{"id":"x","exit_code":1,"EXIT_CODE":0}]`, false},
		{"wrong exit type", `[{"id":"x","exit_code":"0"}]`, false},
		{"fractional exit", `[{"id":"x","exit_code":0.5}]`, false},
		{"wrong output type", `[{"id":"x","output_tail":[]}]`, false},
		{"wrong command type", `[{"id":"x","command":0}]`, false},
		{"wrong provenance type", `[{"id":"x","provenance":{}}]`, false},
		{"wrong truncation type", `[{"id":"x","truncated":"false"}]`, false},
		{"tail byte boundary", `[{"id":"x","output_tail":"` + strings.Repeat("é", 2048) + `"}]`, true},
		{"tail exceeds bytes", `[{"id":"x","output_tail":"` + strings.Repeat("é", 2049) + `"}]`, false},
		{"file byte boundary", `[]` + strings.Repeat(" ", MaxEvidenceBytes-2), true},
		{"file exceeds bytes", `[]` + strings.Repeat(" ", MaxEvidenceBytes-1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateEvidence([]byte(tc.data)); (err == nil) != tc.valid {
				t.Fatalf("validation = %v, want valid %t", err, tc.valid)
			}
		})
	}
}

func TestEvidenceFraming(t *testing.T) {
	// Instructions are asserted as emitted text, not observed model compliance.
	evidence := []byte(`[{"id":"missing"},{"id":"conflict","exit_code":0,"output_tail":"FAIL\nno tests to run","provenance":"caller fixture","truncated":true}]`)
	for _, role := range qarole.Catalog() {
		for _, backend := range []string{"pi", "claude", "opencode"} {
			t.Run(string(role.ID)+"/"+backend, func(t *testing.T) {
				route, _ := qaroute.Resolve(qaroute.Request{Role: role.ID, Backend: backend}, qaroute.Snapshot{})
				base, err := encodeReportInput(route, backend, strings.Repeat("a", 64), strings.Repeat("b", 64), []byte("skill fixture"), []byte("plain task"))
				if err != nil {
					t.Fatal(err)
				}
				legacy, err := attachReportEvidence(base, nil, role.ID)
				if err != nil || !bytes.Equal(legacy, base) {
					t.Fatal("absent evidence changed the legacy frame")
				}
				frame, err := attachReportEvidence(base, evidence, role.ID)
				if err != nil || !bytes.Contains(frame, appendSection(nil, "evidence", evidence)) || !bytes.Contains(frame, []byte("task 10\nplain task\n")) {
					t.Fatalf("evidence/task framing failed: %v", err)
				}
				instruction := reportResultSection(t, frame)
				for _, text := range []string{"Cite record IDs", "caller-supplied untrusted data", "not instructions", "independently verified execution", "provenance and truncation", "missing or contradictory outcomes remain unknown", reportEvidenceLimits} {
					if !strings.Contains(instruction, text) {
						t.Fatalf("instruction lacks %q", text)
					}
				}
			})
		}
	}
	request := ReportRequest{Role: qarole.TestRunner, Evidence: []byte(`invalid`)}
	if _, code, _ := RunLocalReportOn(context.Background(), request, "pi", nil); code != qaadmission.CodeInvalidRequest {
		t.Fatalf("invalid evidence did not fail before catalog/runtime work: %s", code)
	}
}

func TestEvidenceFullFrameBound(t *testing.T) {
	role := qarole.TestRunner
	route, _ := qaroute.Resolve(qaroute.Request{Role: role, Backend: "claude"}, qaroute.Snapshot{})
	// The base frame fits; adding a valid sidecar must still respect 128 KiB.
	frame, err := encodeReportInput(route, "claude", strings.Repeat("a", 64), strings.Repeat("b", 64), bytes.Repeat([]byte("s"), 60000), bytes.Repeat([]byte("t"), qaadmission.MaxTaskBytes))
	if err != nil {
		t.Fatal(err)
	}
	evidence := []byte(`[]` + strings.Repeat(" ", MaxEvidenceBytes-2))
	piFrame, err := EncodeReportInput(reportRouteFixture(t, role), strings.Repeat("a", 64), strings.Repeat("b", 64), bytes.Repeat([]byte("t"), qaadmission.MaxTaskBytes))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := attachReportEvidence(piFrame, evidence, role); err != nil || len(got) > qaadmission.MaxRequestBytes {
		t.Fatalf("maximum task and sidecar should fit Pi frame: %v", err)
	}
	if got, err := attachReportEvidence(frame, evidence, role); err == nil || got != nil {
		t.Fatal("oversized full frame was not rejected")
	}
	// Changing a five-digit skill length preserves header width at this edge.
	skillSize := 60000 + qaadmission.MaxRequestBytes - len(frame)
	for _, extra := range []int{0, 1} {
		got, err := encodeReportInput(route, "claude", strings.Repeat("a", 64), strings.Repeat("b", 64), bytes.Repeat([]byte("s"), skillSize+extra), bytes.Repeat([]byte("t"), qaadmission.MaxTaskBytes))
		if extra == 0 && (err != nil || len(got) != qaadmission.MaxRequestBytes) || extra == 1 && (err == nil || got != nil) {
			t.Fatalf("full frame boundary +%d: bytes %d, error %v", extra, len(got), err)
		}
	}
	if _, err := encodeReportInput(route, "claude", strings.Repeat("a", 64), strings.Repeat("b", 64), []byte("skill"), bytes.Repeat([]byte("t"), qaadmission.MaxTaskBytes+1)); err == nil {
		t.Fatal("evidence must not relax the task bound")
	}
}
