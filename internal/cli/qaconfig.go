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

var qaConfigEfforts = []string{"low", "medium", "high"}

type qaConfigStage int

const (
	qaConfigTable qaConfigStage = iota
	qaConfigBackend
	qaConfigModelList
	qaConfigModelInput
	qaConfigEffort
	qaConfigConfirm
)

// qaConfigModel is the Bubble Tea model of the interactive route configurator.
// It performs no IO itself: models, write and reload are injected so Update
// and View are testable with synthetic keys.
type qaConfigModel struct {
	routes []qaroute.EffectiveRoute
	models func(backend string) ([]string, error)
	write  func(role, backend, provider, model, effort string) error
	reload func() ([]qaroute.EffectiveRoute, error)

	stage   qaConfigStage
	cursor  int // table row
	choice  int // cursor inside the current edit stage
	options []string
	input   string
	backend string
	pair    string
	effort  string
	message string
}

func (m *qaConfigModel) Init() tea.Cmd { return nil }

func (m *qaConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.stage == qaConfigTable {
		switch key.String() {
		case "q", "esc":
			return m, tea.Quit
		case "up", "k":
			m.cursor = max(m.cursor-1, 0)
		case "down", "j":
			m.cursor = min(m.cursor+1, len(m.routes)-1)
		case "enter":
			if len(m.routes) > 0 {
				m.message = ""
				m.startChoice(qaConfigBackend, qaroute.Backends(), m.routes[m.cursor].Backend)
			}
		}
		return m, nil
	}
	if key.Code == tea.KeyEscape {
		m.stage, m.message = qaConfigTable, ""
		return m, nil
	}
	switch m.stage {
	case qaConfigModelInput:
		switch {
		case key.Code == tea.KeyBackspace:
			if runes := []rune(m.input); len(runes) > 0 {
				m.input = string(runes[:len(runes)-1])
			}
		case key.Code == tea.KeyEnter:
			provider, model, found := strings.Cut(m.input, "/")
			if !found || provider == "" || model == "" {
				m.message = "enter <provider>/<model>"
				return m, nil
			}
			m.pair = m.input
			m.chooseEffort()
		case key.Text != "":
			m.input += key.Text
		}
	case qaConfigConfirm:
		switch key.String() {
		case "y":
			m.confirm()
		case "n":
			m.stage = qaConfigTable
		}
	default:
		switch key.String() {
		case "up", "k":
			m.choice = max(m.choice-1, 0)
		case "down", "j":
			m.choice = min(m.choice+1, len(m.options)-1)
		case "enter":
			m.choose(m.options[m.choice])
		}
	}
	return m, nil
}

func (m *qaConfigModel) startChoice(stage qaConfigStage, options []string, current string) {
	m.stage, m.options, m.choice = stage, options, 0
	for index, option := range options {
		if option == current {
			m.choice = index
		}
	}
}

func (m *qaConfigModel) choose(option string) {
	switch m.stage {
	case qaConfigBackend:
		m.backend, m.message = option, ""
		if option != "pi" {
			m.stage, m.input = qaConfigModelInput, ""
			return
		}
		models, err := m.models(option)
		switch {
		case err != nil:
			m.stage, m.message = qaConfigTable, fmt.Sprintf("model detection failed: %v", err)
			return
		case len(models) == 0:
			m.stage, m.message = qaConfigTable, "no models available on "+option
			return
		}
		m.startChoice(qaConfigModelList, models, "")
	case qaConfigModelList:
		m.pair = option
		m.chooseEffort()
	case qaConfigEffort:
		m.effort, m.stage = option, qaConfigConfirm
	}
}

func (m *qaConfigModel) chooseEffort() {
	current := m.current().Effort
	if current == "" {
		current = "high"
	}
	m.startChoice(qaConfigEffort, qaConfigEfforts, current)
}

// current is the effective route of the selected role on the chosen backend.
func (m *qaConfigModel) current() qaroute.EffectiveRoute {
	role := m.routes[m.cursor].Role
	for _, route := range m.routes {
		if route.Role == role && route.Backend == m.backend {
			return route
		}
	}
	return qaroute.EffectiveRoute{Role: role, Backend: m.backend}
}

func (m *qaConfigModel) confirm() {
	provider, model, _ := strings.Cut(m.pair, "/")
	role := string(m.routes[m.cursor].Role)
	m.stage = qaConfigTable
	if err := m.write(role, m.backend, provider, model, m.effort); err != nil {
		m.message = fmt.Sprintf("setup_failed: %v", err)
		return
	}
	m.message = fmt.Sprintf("written: role %s -> %s: %s effort=%s", role, m.backend, m.pair, m.effort)
	routes, err := m.reload()
	if err != nil {
		m.message += fmt.Sprintf(" (refresh failed: %v)", err)
		return
	}
	m.routes = routes
	m.cursor = min(m.cursor, len(routes)-1)
}

func qaConfigRouteText(route qaroute.EffectiveRoute) string {
	if route.Provider == "" {
		return "unset"
	}
	return fmt.Sprintf("%s/%s effort=%s", route.Provider, route.Model, route.Effort)
}

func (m *qaConfigModel) View() tea.View { return tea.NewView(m.render()) }

func (m *qaConfigModel) render() string {
	var view strings.Builder
	switch m.stage {
	case qaConfigTable:
		view.WriteString("QA routes (up/down or j/k select, enter edit, q/esc quit)\n\n")
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
	case qaConfigModelInput:
		fmt.Fprintf(&view, "Model for %s on %s, as <provider>/<model> (enter accept, esc cancel)\n\n> %s\n", m.routes[m.cursor].Role, m.backend, m.input)
	case qaConfigConfirm:
		fmt.Fprintf(&view, "Write route for %s on %s?\n\n  %s -> %s effort=%s\n\n(y write, n/esc cancel)\n",
			m.routes[m.cursor].Role, m.backend, qaConfigRouteText(m.current()), m.pair, m.effort)
	default:
		title := map[qaConfigStage]string{qaConfigBackend: "Backend", qaConfigModelList: "Model", qaConfigEffort: "Effort"}[m.stage]
		fmt.Fprintf(&view, "%s for %s (enter choose, esc cancel)\n\n", title, m.routes[m.cursor].Role)
		for index, option := range m.options {
			marker := "  "
			if index == m.choice {
				marker = "> "
			}
			fmt.Fprintf(&view, "%s%s\n", marker, option)
		}
	}
	if m.message != "" {
		fmt.Fprintf(&view, "\n%s\n", m.message)
	}
	return view.String()
}

// runQAConfigurator opens the interactive configurator on a validated catalog
// root. Writes go through writeQAProfileRole, the `qa setup --role` writer.
func runQAConfigurator(ctx context.Context, catalogRoot string, stdout, stderr io.Writer) int {
	reload := func() ([]qaroute.EffectiveRoute, error) {
		routes, failure := qapi.EffectiveRoutes(catalogRoot, "")
		if failure.Code != "" {
			return nil, fmt.Errorf("%s", failure.Code)
		}
		return routes, nil
	}
	routes, err := reload()
	if err != nil {
		writeError(stderr, err.Error())
		_, _ = io.WriteString(stderr, "next_check=cortex qa routes --catalog "+catalogRoot+"\n")
		return exitFailure
	}
	model := &qaConfigModel{
		routes: routes,
		reload: reload,
		models: func(string) ([]string, error) {
			available, err := qapi.AvailableModels(ctx, qaPiResolver)
			if err != nil {
				return nil, err
			}
			return strings.Fields(sortedModelPairs(available)), nil
		},
		write: func(role, backend, provider, model, effort string) error {
			return writeQAProfileRole(catalogRoot, role, backend, provider, model, effort)
		},
	}
	if _, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(stdout)).Run(); err != nil {
		writeError(stderr, "setup_failed")
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return exitFailure
	}
	return exitOK
}
