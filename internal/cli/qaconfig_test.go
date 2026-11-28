package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/refactor-ia/cortex/internal/qaroute"
)

func qaConfigFixture(t *testing.T) *qaConfigModel {
	t.Helper()
	return &qaConfigModel{routes: []qaroute.EffectiveRoute{
		{Role: "alpha", Backend: "pi", Provider: "openai-codex", Model: "gpt-5", Effort: "high", Source: "profile:qa-fleet"},
		{Role: "alpha", Backend: "claude", Source: "unset"},
		{Role: "beta", Backend: "pi", Provider: "openai-codex", Model: "gpt-5", Effort: "high", Source: "profile:qa-fleet"},
	}}
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
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
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
	model := qaConfigFixture(t)
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

// S6: the table is read-only: enter edits nothing, and q and Esc quit.
func TestQAConfigTableIsReadOnly(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{runeKey("q"), keyEsc} {
		model := qaConfigFixture(t)
		before := model.render()
		qaConfigKeys(t, model, keyEnter)
		if model.render() != before || !qaConfigQuits(qaConfigKeys(t, model, key)) {
			t.Fatalf("%v: enter changed the table or quit=false", key)
		}
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
