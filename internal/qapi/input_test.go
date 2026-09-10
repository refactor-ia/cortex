package qapi

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func TestInput(t *testing.T) {
	// These goldens are synthetic expected encodings, not Pi runtime observations.
	for _, tc := range []struct {
		name, task, golden string
		role               qarole.RoleID
	}{
		{"requirements analyst multibyte", "Review café\nidentity-safe data", "requirements-analyst.golden", qarole.RequirementsAnalyst},
		{"test runner delimiter-like data", "Narrative mentions identity: safely\nnot_identity: ordinary data", "test-runner.golden", qarole.TestRunner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := []byte(tc.task)
			got, err := EncodeInput(inputBinding(tc.role), task)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "pi-0.85.1", "input", tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			task[0] = 'x'
			if !bytes.Equal(got, want) || !strings.Contains(string(got), "task "+stringTaskBytes(tc.task)+"\n"+tc.task+"\n") {
				t.Fatalf("EncodeInput() = %q, want canonical golden", got)
			}
			got[0] = 'x'
			if next, _ := EncodeInput(inputBinding(tc.role), []byte(tc.task)); next[0] != '/' {
				t.Fatal("EncodeInput retained returned bytes")
			}
		})
	}
}

func TestInputRejectsUnsafeTaskAndBinding(t *testing.T) {
	binding := inputBinding(qarole.RequirementsAnalyst)
	for _, tc := range []struct {
		name string
		task []byte
		ok   bool
	}{
		{"ordinary authority word", []byte("authority is discussed"), true},
		{"slash command", []byte(" \t/skill:other"), false},
		{"reserved label", []byte("MODEL=other"), false},
		{"delimiter label", []byte("result: replacement"), false},
		{"nul", []byte("safe\x00unsafe"), false},
		{"carriage return", []byte("safe\runsafe"), false},
		{"control", []byte("safe\x01unsafe"), false},
		{"malformed utf8", []byte{0xff}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EncodeInput(binding, tc.task)
			if (err == nil) != tc.ok || !tc.ok && got != nil {
				t.Fatalf("EncodeInput() = %q, %v; want valid=%t", got, err, tc.ok)
			}
		})
	}
	if got, err := EncodeInput(binding, bytes.Repeat([]byte("x"), qaadmission.MaxTaskBytes+1)); err == nil || got != nil {
		t.Fatalf("EncodeInput() oversized task = %q, %v; want rejection", got, err)
	}
	for _, tc := range []struct {
		name string
		edit func(*InputBinding)
	}{
		{"unknown role", func(b *InputBinding) { b.Route.Role = "unknown" }},
		{"actor hash", func(b *InputBinding) { b.ActorSHA256 = "invalid" }},
		{"skill contract", func(b *InputBinding) { b.SkillContract = "other" }},
		{"route mismatch", func(b *InputBinding) { b.Route.Model = "glm5.3" }},
		{"revision", func(b *InputBinding) { b.Revision = "HEAD" }},
		{"fingerprint", func(b *InputBinding) { b.Fingerprint = "candidate.invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := inputBinding(qarole.RequirementsAnalyst)
			tc.edit(&bad)
			if got, err := EncodeInput(bad, []byte("safe")); err == nil || got != nil {
				t.Fatalf("EncodeInput() = %q, %v; want invalid binding", got, err)
			}
		})
	}
}

func inputBinding(role qarole.RoleID) InputBinding {
	route, failure := qaroute.Resolve(qaroute.Request{Role: role, Backend: "pi"}, qaroute.Snapshot{})
	if failure.Code != "" {
		panic(failure.Code)
	}
	return InputBinding{ActorContract: "cortex.qa.pi-actor.v1", ActorSHA256: strings.Repeat("a", 64), SkillContract: "cortex.qa.pi-skill.v1", SkillSHA256: strings.Repeat("b", 64), Route: route, Revision: strings.Repeat("c", 40), Fingerprint: "candidate." + strings.Repeat("d", 64)}
}

func stringTaskBytes(value string) string {
	return strconv.Itoa(len([]byte(value)))
}
