package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
)

// qaSetupUsageText documents the setup command that writes the user-owned QA
// route profile: the fleet ships no models, so each owner routes with their
// own (issue #298 decision, layer 2).
const qaSetupUsageText = "usage: cortex qa setup --catalog <dir> --provider <p> --model <m> [--effort <e>]\n"

// runQASetup configures the user-owned QA route profile from the models the
// runtime actually offers: it detects the available models on the report
// run's extension-free surface, validates the requested provider/model, and
// writes <catalog>/cortex/qa-profiles.json with one route per fleet role.
// The user never hand-edits JSON (issue #298 decision, layer 2).
func runQASetup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	values := map[string]string{}
	for index := 0; index+1 < len(args); index += 2 {
		flag, value := args[index], args[index+1]
		if value == "" || !strings.HasPrefix(flag, "--") {
			return qaSetupUsage(stderr)
		}
		values[flag] = value
	}
	catalog := values["--catalog"]
	provider := values["--provider"]
	model := values["--model"]
	effort := values["--effort"]
	if effort == "" {
		effort = "high"
	}
	if catalog == "" || provider == "" || model == "" {
		return qaSetupUsage(stderr)
	}
	models, err := qapi.AvailableModels(ctx, qaPiResolver)
	if err != nil {
		writeError(stderr, "setup_failed")
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return exitFailure
	}
	if !models[provider+"\x00"+model] {
		writeError(stderr, "model_unavailable")
		_, _ = io.WriteString(stderr, "available models:\n")
		_, _ = io.WriteString(stderr, sortedModelPairs(models))
		return exitFailure
	}
	if err := writeQAProfile(catalog, provider, model, effort); err != nil {
		writeError(stderr, "setup_failed")
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return exitFailure
	}
	_, _ = fmt.Fprintf(stdout, "qa route profile written to %s (%d roles -> %s/%s effort=%s)\n",
		filepath.Join(catalog, "cortex", "qa-profiles.json"), len(qarole.Catalog()), provider, model, effort)
	return exitOK
}

func qaSetupUsage(stderr io.Writer) int {
	writeError(stderr, "invalid_arguments")
	_, _ = io.WriteString(stderr, qaSetupUsageText)
	return exitUsage
}

// sortedModelPairs renders the runtime's available provider/model pairs as a
// bounded, sorted list for the setup refusal.
func sortedModelPairs(models map[string]bool) string {
	pairs := make([]string, 0, len(models))
	for key := range models {
		pairs = append(pairs, strings.ReplaceAll(key, "\x00", "/"))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "\n") + "\n"
}

// writeQAProfile routes every fleet role to one provider/model/effort and
// writes the user-owned profile file (0600) under the catalog root.
func writeQAProfile(catalog, provider, model, effort string) error {
	routes := make(map[string]map[string]qaProfileRoute, len(qarole.Catalog()))
	for _, contract := range qarole.Catalog() {
		routes[string(contract.ID)] = map[string]qaProfileRoute{"pi": {Provider: provider, Model: model, Effort: effort}}
	}
	profile := qaProfileFile{
		SchemaVersion:  1,
		DefaultProfile: "qa-fleet",
		Profiles:       map[string]qaProfile{"qa-fleet": {Routes: routes}},
	}
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Join(catalog, "cortex")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "qa-profiles.json"), append(encoded, '\n'), 0o600)
}

type qaProfileRoute struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
}

type qaProfile struct {
	Routes map[string]map[string]qaProfileRoute `json:"routes"`
}

type qaProfileFile struct {
	SchemaVersion  int                  `json:"schemaVersion"`
	DefaultProfile string               `json:"defaultProfile"`
	Profiles       map[string]qaProfile `json:"profiles"`
}
