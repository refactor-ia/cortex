package qapi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

func TestProbeModel(t *testing.T) {
	observed := readProbeFixture(t, "models/model-list.txt")
	tests := []struct {
		name      string
		input     ModelProbeInput
		provider  string
		model     string
		available bool
		code      qaadmission.Code
	}{
		{"observed exact row is available", modelInput(observed), "nan", "qwen3.6", true, ""},
		{"observed missing model is unavailable", modelInput(observed), "nan", "glm9.9-absent", false, qaadmission.CodeModelUnavailable},
		{"wrong provider does not match", modelInput(observed), "nan-display", "qwen3.6", false, qaadmission.CodeModelUnavailable},
		{"display label does not match", modelInput(observed), "nan", "Qwen 3.6", false, qaadmission.CodeModelUnavailable},
		{"dynamic header whitespace is accepted", modelInput([]byte(" \tprovider\t model  context\tmax-out thinking images \t\r\nexample-provider example-model 262.1K 16.4K yes yes\r\n")), "example-provider", "example-model", true, ""},
		{"missing requested model remains unavailable", modelInput([]byte("provider model context max-out thinking images\nexample-provider example-model 262.1K 16.4K yes yes\n")), "example-provider", "glm9.9-absent", false, qaadmission.CodeModelUnavailable},
		{"wrong header token fails normalization", modelInput([]byte("provider model context max-out thinking images-wrong\nexample-provider example-model 262.1K 16.4K yes yes\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"missing header token fails normalization", modelInput([]byte("provider model context max-out thinking\nexample-provider example-model 262.1K 16.4K yes yes\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"extra header token fails normalization", modelInput([]byte("provider model context max-out thinking images extra\nexample-provider example-model 262.1K 16.4K yes yes\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"reordered header tokens fail normalization", modelInput([]byte("model provider context max-out thinking images\nexample-provider example-model 262.1K 16.4K yes yes\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"header-only table remains unavailable", modelInput([]byte(modelTableHeader + "\n")), "example-provider", "example-model", false, qaadmission.CodeModelUnavailable},
		{"five-field row fails normalization", modelInput([]byte("provider model context max-out thinking images\nexample-provider example-model 262.1K 16.4K yes\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"seven-field row fails normalization", modelInput([]byte("provider model context max-out thinking images\nexample-provider example-model 262.1K 16.4K yes yes extra\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"duplicate provider model fails normalization", modelInput(append(append([]byte{}, observed...), []byte("nan           qwen3.6              262.1K   16.4K    yes       yes   \n")...)), "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"malformed table fails normalization", modelInput([]byte("provider model\n")), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"invalid UTF-8 fails normalization", modelInput([]byte{0xff}), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"overflow fails normalization", modelInput(bytes.Repeat([]byte("x"), maxModelOutputBytes+1)), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
		{"nonzero exit fails normalization", ModelProbeInput{Stdout: observed, ExitCode: 1, Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"truncated capture fails normalization", ModelProbeInput{Stdout: observed, Complete: false}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"silent output fails normalization", modelInput(nil), "example-provider", "example-model", false, qaadmission.CodeNormalizationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProbeModel(tt.input, tt.provider, tt.model)
			if result.Available != tt.available || result.Code != tt.code {
				t.Fatalf("ProbeModel() = %#v, want available=%t code=%q", result, tt.available, tt.code)
			}
		})
	}
}

func TestProbeAuth(t *testing.T) {
	ready := readProbeFixture(t, "auth/ready.json")
	tests := []struct {
		name  string
		input AuthProbeInput
		ready bool
		code  qaadmission.Code
	}{
		{"observed ready auth passes", routeAuthInput("nan", ready, 0), true, ""},
		{"not ready with source reason is not ready", authInput([]byte(`{"status":"not_ready","reason":"credential unavailable"}`), 1), false, qaadmission.CodeAuthNotReady},
		{"not ready wrong exit fails normalization", authInput([]byte(`{"status":"not_ready","reason":"credential unavailable"}`), 0), false, qaadmission.CodeNormalizationFailed},
		{"wrong provider fails normalization", authInput([]byte(`{"status":"ready","provider":"other","authType":"api_key"}`), 0), false, qaadmission.CodeNormalizationFailed},
		{"unknown auth type fails normalization", authInput([]byte(`{"status":"ready","provider":"example-provider","authType":"token"}`), 0), false, qaadmission.CodeNormalizationFailed},
		{"invalid status fails normalization", authInput([]byte(`{"status":"invalid","reason":"invalid credential"}`), 2), false, qaadmission.CodeNormalizationFailed},
		{"missing no refresh evidence fails normalization", AuthProbeInput{Provider: "nan", Stdout: ready, Complete: true, ExitCode: 0}, false, qaadmission.CodeNormalizationFailed},
		{"duplicate JSON key fails normalization", authInput([]byte(`{"status":"ready","status":"ready","provider":"example-provider","authType":"api_key"}`), 0), false, qaadmission.CodeNormalizationFailed},
		{"unknown JSON field fails normalization", authInput([]byte(`{"status":"ready","provider":"example-provider","authType":"api_key","extra":"x"}`), 0), false, qaadmission.CodeNormalizationFailed},
		{"trailing JSON fails normalization", authInput(append(append([]byte{}, ready...), []byte(` {}`)...), 0), false, qaadmission.CodeNormalizationFailed},
		{"primitive JSON fails normalization", authInput([]byte(`true`), 0), false, qaadmission.CodeNormalizationFailed},
		{"invalid UTF-8 fails normalization", authInput([]byte{0xff}, 0), false, qaadmission.CodeNormalizationFailed},
		{"overflow fails normalization", authInput(bytes.Repeat([]byte("x"), maxAuthOutputBytes+1), 0), false, qaadmission.CodeNormalizationFailed},
		{"truncated capture fails normalization", AuthProbeInput{Provider: "nan", Stdout: ready, ExitCode: 0, NoRefresh: true}, false, qaadmission.CodeNormalizationFailed},
		{"silent output fails normalization", authInput(nil, 0), false, qaadmission.CodeNormalizationFailed},
		{"oauth ready for the route provider passes", routeAuthInput("openai-codex", []byte(`{"status":"ready","provider":"openai-codex","authType":"oauth"}`), 0), true, ""},
		{"ready for another provider than the route fails normalization", routeAuthInput("openai-codex", ready, 0), false, qaadmission.CodeNormalizationFailed},
		{"not ready for the route provider is not ready", routeAuthInput("openai-codex", []byte(`{"status":"not_ready","provider":"openai-codex","reason":"credentials_not_configured"}`), 1), false, qaadmission.CodeAuthNotReady},
		{"missing route provider fails normalization", AuthProbeInput{Stdout: ready, Complete: true, NoRefresh: true}, false, qaadmission.CodeNormalizationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProbeAuth(tt.input)
			if result.Ready != tt.ready || result.Code != tt.code {
				t.Fatalf("ProbeAuth() = %#v, want ready=%t code=%q", result, tt.ready, tt.code)
			}
		})
	}
}

func modelInput(stdout []byte) ModelProbeInput {
	return ModelProbeInput{Stdout: stdout, Complete: true}
}

func authInput(stdout []byte, exitCode int) AuthProbeInput {
	return routeAuthInput("example-provider", stdout, exitCode)
}

func routeAuthInput(provider string, stdout []byte, exitCode int) AuthProbeInput {
	return AuthProbeInput{Provider: provider, Stdout: stdout, ExitCode: exitCode, Complete: true, NoRefresh: true}
}

func readProbeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(backendTestdata(piFixtureBackend, piFixtureVersion, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
