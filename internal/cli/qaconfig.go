package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// qaIsTerminal reports whether stdin and stdout are both terminals; only then
// may `qa setup --catalog <dir>` open the interactive configurator (#454).
var qaIsTerminal = func() bool {
	for _, file := range []*os.File{os.Stdin, os.Stdout} {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

// qaConfigModel is the Bubble Tea model of the read-only route table. It
// performs no IO itself, so Update and View are testable with synthetic keys.
type qaConfigModel struct {
	routes []qaroute.EffectiveRoute
	cursor int // table row
}

func (m *qaConfigModel) Init() tea.Cmd { return nil }

func (m *qaConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, len(m.routes)-1)
	}
	return m, nil
}

func (m *qaConfigModel) View() tea.View { return tea.NewView(m.render()) }

func (m *qaConfigModel) render() string {
	var view strings.Builder
	view.WriteString("QA routes (up/down or j/k select, q/esc quit)\n\n")
	fmt.Fprintf(&view, "  %-28s %-9s %-40s %-7s %s\n", "ROLE", "BACKEND", "PROVIDER/MODEL", "EFFORT", "SOURCE")
	for index, route := range m.routes {
		marker := "  "
		if index == m.cursor {
			marker = "> "
		}
		pair := "-"
		if route.Provider != "" {
			pair = route.Provider + "/" + route.Model
		}
		fmt.Fprintf(&view, "%s%-28s %-9s %-40s %-7s %s\n", marker, route.Role, route.Backend, pair, dashIfEmpty(route.Effort), route.Source)
	}
	return view.String()
}

// runQAConfigurator opens the interactive route table on a validated catalog
// root. It is read-only: quitting writes nothing.
func runQAConfigurator(ctx context.Context, catalogRoot string, stdout, stderr io.Writer) int {
	routes, failure := qapi.EffectiveRoutes(catalogRoot, "")
	if failure.Code != "" {
		writeError(stderr, failure.Code)
		_, _ = io.WriteString(stderr, "next_check=cortex qa routes --catalog "+catalogRoot+"\n")
		return exitFailure
	}
	model := &qaConfigModel{routes: routes}
	if _, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(stdout)).Run(); err != nil {
		writeError(stderr, "setup_failed")
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return exitFailure
	}
	return exitOK
}
