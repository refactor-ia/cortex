package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/qapi"
)

const qaRoutesUsageLine = "usage: cortex qa routes --catalog <dir> [--profile <id>]\n"

// runQARoutes prints the effective route of every QA role on every admitted
// backend and where it comes from (#457). It reads through the same
// resolution `qa run` uses and writes nothing; a refused profile reports the
// diagnostic `qa run` reports for it.
func runQARoutes(args []string, stdout, stderr io.Writer) int {
	catalog, profile := "", ""
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) || args[index+1] == "" {
			return qaRoutesUsage(stderr)
		}
		switch {
		case args[index] == "--catalog" && catalog == "":
			catalog = args[index+1]
		case args[index] == "--profile" && profile == "":
			profile = args[index+1]
		default:
			return qaRoutesUsage(stderr)
		}
	}
	if catalog == "" {
		return qaRoutesUsage(stderr)
	}
	catalogRoot, err := filepath.Abs(catalog)
	if err != nil {
		return qaFailure(stderr, "invalid_request")
	}
	routes, failure := qapi.EffectiveRoutes(catalogRoot, profile)
	if failure.Code != "" {
		code := qaFailure(stderr, failure.Code)
		if stage := qaRefusalStage(failure.Code, nil); stage != "" {
			_, _ = fmt.Fprintf(stderr, "stage=%s\n", stage)
		}
		if failure.Code == "profile_invalid" {
			_, _ = io.WriteString(stderr, qaProfileDiagnostic(profile, catalogRoot))
		}
		return code
	}
	for _, route := range routes {
		provider, model, effort := dashIfEmpty(route.Provider), dashIfEmpty(route.Model), dashIfEmpty(route.Effort)
		_, _ = fmt.Fprintf(stdout, "role=%s backend=%s provider=%s model=%s effort=%s source=%s\n",
			route.Role, route.Backend, sanitizeRouteIdentity(provider), sanitizeRouteIdentity(model), sanitizeRouteIdentity(effort), route.Source)
	}
	return exitOK
}

func dashIfEmpty(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func qaRoutesUsage(stderr io.Writer) int {
	writeError(stderr, "invalid_arguments")
	_, _ = io.WriteString(stderr, qaUsage)
	return exitUsage
}
