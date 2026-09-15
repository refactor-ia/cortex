package qapi

import (
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func TestAdmissionRequest(t *testing.T) {
	valid := admissionRequestJSON()
	for _, tc := range []struct {
		name  string
		input string
		want  AdmissionRequest
		ok    bool
	}{
		{"complete request", valid, AdmissionRequest{
			Role: qarole.RequirementsAnalyst, Backend: "pi", CurrentDirectory: "/worktree",
			Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64),
			Task: "Please write a review.", Profile: "balanced",
			Override: qaroute.Override{Provider: "nan", Model: "qwen3.6", Effort: "high"}, TimeoutSeconds: 30,
		}, true},
		{"defaults", `{"schemaVersion":1,"contract":"cortex.qa.pi-admission-request.v1","role":"test-runner","backend":"pi","currentDirectory":"/worktree","revision":"` + strings.Repeat("a", 40) + `","fingerprint":"candidate.` + strings.Repeat("b", 64) + `","task":"Please write a review."}`, AdmissionRequest{
			Role: qarole.TestRunner, Backend: "pi", CurrentDirectory: "/worktree",
			Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64),
			Task: "Please write a review.", TimeoutSeconds: 900,
		}, true},
		{"maximum timeout", strings.Replace(valid, `"timeoutSeconds":30`, `"timeoutSeconds":3600`, 1), AdmissionRequest{
			Role: qarole.RequirementsAnalyst, Backend: "pi", CurrentDirectory: "/worktree",
			Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64),
			Task: "Please write a review.", Profile: "balanced",
			Override: qaroute.Override{Provider: "nan", Model: "qwen3.6", Effort: "high"}, TimeoutSeconds: 3600,
		}, true},
		{"neither branch", `{"schemaVersion":1,"role":"requirements-analyst"}`, AdmissionRequest{}, false},
		{"both branches", strings.Replace(valid, `"contract":`, `"optIn":"cortex.qa.external.v1","contract":`, 1), AdmissionRequest{}, false},
		{"reserved external branch", strings.Replace(valid, `"contract":"cortex.qa.pi-admission-request.v1"`, `"optIn":"cortex.qa.external.v1"`, 1), AdmissionRequest{}, false},
		{"unknown contract", strings.Replace(valid, "cortex.qa.pi-admission-request.v1", "unknown", 1), AdmissionRequest{}, false},
		{"unknown version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1), AdmissionRequest{}, false},
		{"duplicate branch marker", strings.Replace(valid, `"contract":`, `"contract":"cortex.qa.pi-admission-request.v1","contract":`, 1), AdmissionRequest{}, false},
		{"cross branch field", strings.Replace(valid, `"task":`, `"optIn":"cortex.qa.external.v1","task":`, 1), AdmissionRequest{}, false},
		{"root array", `[]`, AdmissionRequest{}, false},
		{"trailing JSON", valid + ` {}`, AdmissionRequest{}, false},
		{"unknown field", strings.Replace(valid, `}`, `,"tools":["read"]}`, 1), AdmissionRequest{}, false},
		{"unknown override field", strings.Replace(valid, `"effort":"high"`, `"effort":"high","tools":"read"`, 1), AdmissionRequest{}, false},
		{"duplicate override field", strings.Replace(valid, `"provider":"nan",`, `"provider":"nan","provider":"nan",`, 1), AdmissionRequest{}, false},
		{"override array", strings.Replace(valid, `"provider":"nan"`, `"provider":["nan"]`, 1), AdmissionRequest{}, false},
		{"task array", strings.Replace(valid, `"task":"Please write a review."`, `"task":[]`, 1), AdmissionRequest{}, false},
		{"invalid profile", strings.Replace(valid, `"profile":"balanced"`, `"profile":"Balanced"`, 1), AdmissionRequest{}, false},
		{"escaped task nul", strings.Replace(valid, "Please write a review.", `safe\u0000unsafe`, 1), AdmissionRequest{}, false},
		{"invalid role", strings.Replace(valid, "requirements-analyst", "other", 1), AdmissionRequest{}, false},
		{"alternate backend", strings.Replace(valid, `"backend":"pi"`, `"backend":"other"`, 1), AdmissionRequest{}, false},
		{"noncanonical path", strings.Replace(valid, `"currentDirectory":"/worktree"`, `"currentDirectory":"/worktree/../other"`, 1), AdmissionRequest{}, false},
		{"invalid revision", strings.Replace(valid, strings.Repeat("a", 40), "HEAD", 1), AdmissionRequest{}, false},
		{"invalid fingerprint", strings.Replace(valid, "candidate."+strings.Repeat("b", 64), "candidate.invalid", 1), AdmissionRequest{}, false},
		{"unsafe task", strings.Replace(valid, "Please write a review.", "tools: write", 1), AdmissionRequest{}, false},
		{"timeout below bound", strings.Replace(valid, `"timeoutSeconds":30`, `"timeoutSeconds":29`, 1), AdmissionRequest{}, false},
		{"timeout above bound", strings.Replace(valid, `"timeoutSeconds":30`, `"timeoutSeconds":3601`, 1), AdmissionRequest{}, false},
		{"timeout wrong type", strings.Replace(valid, `"timeoutSeconds":30`, `"timeoutSeconds":"30"`, 1), AdmissionRequest{}, false},
		{"task exceeds bound", strings.Replace(valid, "Please write a review.", strings.Repeat("x", qaadmission.MaxTaskBytes+1), 1), AdmissionRequest{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeAdmissionRequest([]byte(tc.input))
			if (err == nil) != tc.ok {
				t.Fatalf("DecodeAdmissionRequest() error = %v, want valid=%t", err, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("DecodeAdmissionRequest() = %#v, want %#v", got, tc.want)
			}
		})
	}

	for _, input := range [][]byte{
		append([]byte(admissionRequestJSON()), 0),
		append([]byte(admissionRequestJSON()), 0xff),
		append([]byte(admissionRequestJSON()), strings.Repeat(" ", qaadmission.MaxRequestBytes)...),
	} {
		if got, err := DecodeAdmissionRequest(input); err == nil || got != (AdmissionRequest{}) {
			t.Fatalf("DecodeAdmissionRequest() = %#v, %v; want rejection", got, err)
		}
	}
}

func admissionRequestJSON() string {
	return `{"schemaVersion":1,"contract":"cortex.qa.pi-admission-request.v1","role":"requirements-analyst","backend":"pi","currentDirectory":"/worktree","revision":"` + strings.Repeat("a", 40) + `","fingerprint":"candidate.` + strings.Repeat("b", 64) + `","task":"Please write a review.","profile":"balanced","override":{"provider":"nan","model":"qwen3.6","effort":"high"},"timeoutSeconds":30}`
}
