package cli

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/refactor-ia/cortex/internal/qaroute"
)

type qaConfigWrite struct{ role, backend, provider, model, effort string }

func qaConfigFixture(t *testing.T) (*qaConfigModel, *[]qaConfigWrite) {
	t.Helper()
	routes := []qaroute.EffectiveRoute{
		{Role: "alpha", Backend: "pi", Provider: "openai-codex", Model: "gpt-5", Effort: "high", Source: "profile:qa-fleet"},
		{Role: "alpha", Backend: "claude", Source: "unset"},
		{Role: "beta", Backend: "pi", Provider: "openai-codex", Model: "gpt-5", Effort: "high", Source: "profile:qa-fleet"},
	}
	writes := &[]qaConfigWrite{}
	model := &qaConfigModel{
		routes: routes,
		models: func(backend string) ([]string, error) {
			if backend != "pi" {
				t.Fatalf("models probed for %s", backend)
			}
			return []string{"anthropic/sonnet", "openai-codex/gpt-5"}, nil
		},
		write: func(role, backend, provider, model, effort string) error {
			*writes = append(*writes, qaConfigWrite{role, backend, provider, model, effort})
			return nil
		},
		reload: func() ([]qaroute.EffectiveRoute, error) {
			reloaded := append([]qaroute.EffectiveRoute(nil), routes...)
			reloaded[2] = qaroute.EffectiveRoute{Role: "beta", Backend: "pi", Provider: "anthropic", Model: "sonnet", Effort: "low", Source: "profile:qa-fleet"}
			return reloaded, nil
		},
	}
	return model, writes
}

func qaConfigKeys(t *testing.T, model *qaConfigModel, keys ...tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, key := range keys {
		var next tea.Model
		next, cmd = model.Update(key)
		*model = *next.(*qaConfigModel)
	}
	return cmd
}

func runeKey(value string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(value)[0], Text: value}
}

var (
	keyDown      = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUp        = tea.KeyPressMsg{Code: tea.KeyUp}
	keyEnter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc       = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyBackspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func qaConfigQuits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// S6: the table lists every role x backend route with its source; j/k and
// arrows move the selection.
func TestQAConfigTableShowsRoutesAndNavigates(t *testing.T) {
	model, _ := qaConfigFixture(t)
	view := model.render()
	for _, want := range []string{"alpha", "beta", "claude", "openai-codex/gpt-5", "profile:qa-fleet", "unset"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	qaConfigKeys(t, model, runeKey("j"), keyDown, keyDown)
	if model.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (clamped)", model.cursor)
	}
	qaConfigKeys(t, model, runeKey("k"), keyUp, keyUp)
	if model.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", model.cursor)
	}
}

// S6: q and Esc quit the table without writing.
func TestQAConfigQuitWritesNothing(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{runeKey("q"), keyEsc} {
		model, writes := qaConfigFixture(t)
		if !qaConfigQuits(qaConfigKeys(t, model, key)) || len(*writes) != 0 {
			t.Fatalf("%v: quit=false or writes=%v", key, *writes)
		}
	}
}

// S6: role -> backend -> pi model from the runtime list -> effort -> confirm
// writes exactly once through the per-role writer and refreshes the table.
func TestQAConfigConfirmWritesSelectedRoute(t *testing.T) {
	model, writes := qaConfigFixture(t)
	qaConfigKeys(t, model, keyDown, keyDown, keyEnter) // beta/pi
	qaConfigKeys(t, model, keyEnter)                   // backend pi (preselected)
	if !strings.Contains(model.render(), "anthropic/sonnet") {
		t.Fatalf("model list view = %s", model.render())
	}
	qaConfigKeys(t, model, keyEnter)               // anthropic/sonnet
	qaConfigKeys(t, model, keyUp, keyUp, keyEnter) // effort low
	view := model.render()
	if !strings.Contains(view, "openai-codex/gpt-5 effort=high -> anthropic/sonnet effort=low") {
		t.Fatalf("confirm view = %s", view)
	}
	if len(*writes) != 0 {
		t.Fatalf("wrote before confirmation: %v", *writes)
	}
	qaConfigKeys(t, model, runeKey("y"))
	want := []qaConfigWrite{{"beta", "pi", "anthropic", "sonnet", "low"}}
	if len(*writes) != 1 || (*writes)[0] != want[0] {
		t.Fatalf("writes = %v, want %v", *writes, want)
	}
	if model.stage != qaConfigTable || model.routes[2].Model != "sonnet" {
		t.Fatalf("stage = %v routes = %v, want refreshed table", model.stage, model.routes)
	}
}

// S6: non-pi backends take a typed provider/model.
func TestQAConfigTypedModelForClaude(t *testing.T) {
	model, writes := qaConfigFixture(t)
	qaConfigKeys(t, model, keyDown, keyEnter, keyEnter) // alpha/claude, backend claude
	qaConfigKeys(t, model, runeKey("anthropic/opux"), keyBackspace, runeKey("s"), keyEnter)
	qaConfigKeys(t, model, keyEnter, runeKey("y")) // effort high (default)
	want := qaConfigWrite{"alpha", "claude", "anthropic", "opus", "high"}
	if len(*writes) != 1 || (*writes)[0] != want {
		t.Fatalf("writes = %v, want %v", *writes, want)
	}
}

// S6: cancelling at any edit stage or declining the confirmation writes
// nothing and returns to the table.
func TestQAConfigCancelWritesNothing(t *testing.T) {
	sequences := [][]tea.KeyPressMsg{
		{keyEnter, keyEsc},
		{keyEnter, keyEnter, keyEsc},
		{keyEnter, keyEnter, keyEnter, keyEsc},
		{keyEnter, keyEnter, keyEnter, keyEnter, runeKey("n")},
		{keyEnter, keyEnter, keyEnter, keyEnter, keyEsc},
	}
	for index, keys := range sequences {
		model, writes := qaConfigFixture(t)
		if qaConfigQuits(qaConfigKeys(t, model, keys...)) {
			t.Fatalf("sequence %d quit", index)
		}
		if len(*writes) != 0 || model.stage != qaConfigTable {
			t.Fatalf("sequence %d: writes=%v stage=%v", index, *writes, model.stage)
		}
	}
}

// S6: a writer failure is shown and the table is kept.
func TestQAConfigWriteFailureIsReported(t *testing.T) {
	model, _ := qaConfigFixture(t)
	model.write = func(string, string, string, string, string) error { return errors.New("disk full") }
	qaConfigKeys(t, model, keyEnter, keyEnter, keyEnter, keyEnter, runeKey("y"))
	if !strings.Contains(model.render(), "disk full") {
		t.Fatalf("view = %s", model.render())
	}
}

// S6: backspace removes a whole rune from a multibyte typed model.
func TestQAConfigBackspaceRemovesRune(t *testing.T) {
	model, writes := qaConfigFixture(t)
	qaConfigKeys(t, model, keyDown, keyEnter, keyEnter) // alpha/claude
	qaConfigKeys(t, model, runeKey("anthropic/ópusé"), keyBackspace, keyEnter, keyEnter, runeKey("y"))
	want := qaConfigWrite{"alpha", "claude", "anthropic", "ópus", "high"}
	if len(*writes) != 1 || (*writes)[0] != want {
		t.Fatalf("writes = %v, want %v", *writes, want)
	}
}

// S6: an empty pi model list says so instead of printing a nil error.
func TestQAConfigEmptyModelList(t *testing.T) {
	model, writes := qaConfigFixture(t)
	model.models = func(string) ([]string, error) { return nil, nil }
	qaConfigKeys(t, model, keyEnter, keyEnter)
	view := model.render()
	if model.stage != qaConfigTable || !strings.Contains(view, "no models available") || strings.Contains(view, "<nil>") || len(*writes) != 0 {
		t.Fatalf("stage=%v writes=%v view=%s", model.stage, *writes, view)
	}
}

// S6: without a TTY, `qa setup --catalog` keeps the scripting usage refusal.
func TestQASetupWithoutTTYKeepsUsage(t *testing.T) {
	original := qaIsTerminal
	qaIsTerminal = func() bool { return false }
	t.Cleanup(func() { qaIsTerminal = original })
	catalog := qaSetupCatalogFixture(t)
	for _, args := range [][]string{{"--catalog", catalog}, {"--catalog=" + catalog}, {"--catalog", catalog, "--effort", "low"}} {
		code, stdout, stderr := runQASetupFixture(t, args...)
		if code != exitUsage || stdout != "" || stderr != "error=invalid_arguments\n"+qaSetupUsageLine {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}
