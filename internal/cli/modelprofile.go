package cli

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/atomicfile"
	"github.com/refactor-ia/cortex/internal/modelprofile"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

var (
	errUnsupportedOpenCodeOverride = errors.New("unsupported OpenCode config override")
	errProfileIntervention         = errors.New("model profile intervention required")
)

func runModelProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "profile" {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	if args[1] == "status" && (len(args) == 2 || len(args) == 4 && args[2] == "--scope" && args[3] == "user") {
		if profileStatus(stdout) == nil {
			return exitOK
		}
	}
	if args[1] == "apply" && len(args) >= 3 && (args[2] == "nan" || args[2] == "mixed" || args[2] == "openai") {
		if runtime, ok := profileWriteFlags(args[3:]); ok {
			err := profileApply(modelprofile.Profile(args[2]), runtime)
			if err == nil {
				_, _ = io.WriteString(stdout, "scope=user_config_only status=applied\n")
				return exitOK
			}
			if errors.Is(err, errUnsupportedOpenCodeOverride) {
				writeError(stderr, "model_profile_unsupported_opencode_override")
			} else if errors.Is(err, errProfileIntervention) {
				writeError(stderr, "model_profile_intervention_required")
			} else {
				writeError(stderr, "model_profile_apply_failed")
			}
			return exitFailure
		}
	}
	if args[1] == "rollback" {
		if runtime, ok := profileWriteFlags(args[2:]); ok {
			if profileRollback(runtime) == nil {
				_, _ = io.WriteString(stdout, "scope=user_config_only status=rolled_back\n")
				return exitOK
			}
			writeError(stderr, "model_profile_rollback_failed")
			return exitFailure
		}
	}
	writeError(stderr, "invalid_command")
	return exitUsage
}

func profileWriteFlags(args []string) (string, bool) {
	if len(args) < 2 || len(args) > 4 {
		return "", false
	}
	scope, runtime := false, ""
	for len(args) > 0 {
		if args[0] == "--scope" && len(args) > 1 && args[1] == "user" && !scope {
			scope = true
			args = args[2:]
			continue
		}
		if args[0] == "--runtime" && len(args) > 1 && runtime == "" && (args[1] == "pi" || args[1] == "opencode") {
			runtime = args[1]
			args = args[2:]
			continue
		}
		return "", false
	}
	return runtime, scope
}

func profileRoots() (modelprofile.RuntimeRoots, error) {
	all, err := skillroot.ResolveSystemUninstallRoots()
	if err != nil {
		return modelprofile.RuntimeRoots{}, err
	}
	var roots modelprofile.RuntimeRoots
	for _, root := range all {
		if root.RuntimeID() == runtimematrix.RuntimePi {
			roots.Pi = root.RootPath()
		}
		if root.RuntimeID() == runtimematrix.RuntimeOpenCode {
			roots.OpenCode = root.RootPath()
		}
	}
	return roots, nil
}

func profileApply(profile modelprofile.Profile, requested string) error {
	roots, err := profileRoots()
	if err != nil {
		return err
	}
	changes, err := profileChanges(roots, profile, requested)
	if err != nil || len(changes) == 0 {
		return err
	}
	state, err := profileStateDir()
	if err != nil {
		return err
	}
	txn, err := os.MkdirTemp(state, "txn-")
	if err != nil {
		return err
	}
	if err = os.Chmod(txn, 0o700); err != nil {
		return err
	}
	if err = modelprofile.Save(txn, roots, changes); err != nil {
		return err
	}
	backup, err := modelprofile.Load(txn)
	if err != nil {
		return err
	}
	if err = modelprofile.ApplySaved(roots, backup); err != nil {
		return err
	}
	if err = atomicfile.Replace(state, "current", []byte(filepath.Base(txn)), 0o600); err != nil {
		if modelprofile.Restore(roots, backup) != nil {
			return errProfileIntervention
		}
		return err
	}
	return nil
}

func profileChanges(roots modelprofile.RuntimeRoots, profile modelprofile.Profile, requested string) ([]modelprofile.Change, error) {
	pi, open, err := profileSelected(roots, requested)
	if err != nil {
		return nil, err
	}
	var changes []modelprofile.Change
	if pi {
		settings, smode, err := profileFile(roots.Pi, "settings.json")
		if err != nil {
			return nil, err
		}
		subagents, samode, err := profileFile(roots.Pi, "subagents.json")
		if err != nil {
			return nil, err
		}
		newSettings, newSubagents, err := modelprofile.TransformPi(settings, subagents, profile)
		if err != nil {
			return nil, err
		}
		if string(settings) != string(newSettings) {
			changes = append(changes, modelprofile.Change{Target: modelprofile.PiSettings, After: newSettings, AfterMode: smode})
		}
		if string(subagents) != string(newSubagents) {
			changes = append(changes, modelprofile.Change{Target: modelprofile.PiSubagents, After: newSubagents, AfterMode: samode})
		}
	}
	if open {
		config, mode, err := profileFile(roots.OpenCode, "opencode.json")
		if err != nil {
			return nil, err
		}
		candidate, err := modelprofile.TransformOpenCode(config, profile)
		if err != nil {
			return nil, err
		}
		if string(config) != string(candidate) {
			changes = append(changes, modelprofile.Change{Target: modelprofile.OpenCodeConfig, After: candidate, AfterMode: mode})
		}
	}
	return changes, nil
}

func profileSelected(roots modelprofile.RuntimeRoots, requested string) (bool, bool, error) {
	pi, err := profileRootPresent(roots.Pi)
	if err != nil {
		return false, false, err
	}
	if pi {
		if _, _, err = profileFile(roots.Pi, "settings.json"); err != nil {
			return false, false, err
		}
		if _, _, err = profileFile(roots.Pi, "subagents.json"); err != nil {
			return false, false, err
		}
	}
	open, err := profileRootPresent(roots.OpenCode)
	if err != nil {
		return false, false, err
	}
	if open {
		if os.Getenv("OPENCODE_CONFIG") != "" {
			return false, false, errUnsupportedOpenCodeOverride
		}
		if _, err = os.Lstat(filepath.Join(roots.OpenCode, "opencode.jsonc")); err == nil {
			return false, false, errUnsupportedOpenCodeOverride
		} else if !os.IsNotExist(err) {
			return false, false, err
		}
		if _, _, err = profileFile(roots.OpenCode, "opencode.json"); os.IsNotExist(err) {
			open = false
		} else if err != nil {
			return false, false, err
		}
	}
	if requested == "pi" {
		if pi {
			return true, false, nil
		}
		return false, false, os.ErrNotExist
	}
	if requested == "opencode" {
		if open {
			return false, true, nil
		}
		return false, false, os.ErrNotExist
	}
	if !pi && !open {
		return false, false, os.ErrNotExist
	}
	return pi, open, nil
}

func profileRootPresent(root string) (bool, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, err
	}
	return true, nil
}
func profileFile(root, leaf string) ([]byte, fs.FileMode, error) {
	path := filepath.Join(root, leaf)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, 0, err
	}
	data, err := os.ReadFile(path)
	return data, info.Mode().Perm(), err
}

func profileStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	state := filepath.Join(home, ".cortex", "model-profile")
	if err = os.MkdirAll(state, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", os.ErrInvalid
	}
	return state, os.Chmod(state, 0o700)
}
func profileRollback(requested string) error {
	roots, err := profileRoots()
	if err != nil {
		return err
	}
	state, err := profileStateDir()
	if err != nil {
		return err
	}
	data, mode, err := profileFile(state, "current")
	if err != nil || mode != 0o600 {
		return os.ErrInvalid
	}
	name := string(data)
	if !strings.HasPrefix(name, "txn-") || filepath.Base(name) != name {
		return os.ErrInvalid
	}
	txn := filepath.Join(state, name)
	info, err := os.Lstat(txn)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return os.ErrInvalid
	}
	backup, err := modelprofile.Load(txn)
	if err != nil {
		return err
	}
	if requested != "" && !backupIsOnly(backup, requested) {
		return os.ErrInvalid
	}
	return modelprofile.Restore(roots, backup)
}
func backupIsOnly(backup modelprofile.Backup, runtime string) bool {
	for _, target := range modelprofile.BackupTargets(backup) {
		if runtime == "pi" && target == modelprofile.OpenCodeConfig || runtime == "opencode" && target != modelprofile.OpenCodeConfig {
			return false
		}
	}
	return true
}

func profileStatus(stdout io.Writer) error {
	roots, err := profileRoots()
	if err != nil {
		return err
	}
	pi, open, err := profileSelected(roots, "")
	if err != nil {
		return err
	}
	if pi {
		if err = writeProfilePrimary(stdout, "pi", roots.Pi, "settings.json"); err != nil {
			return err
		}
	}
	if open {
		err = writeProfilePrimary(stdout, "opencode", roots.OpenCode, "opencode.json")
	}
	return err
}
func writeProfilePrimary(out io.Writer, runtime, root, leaf string) error {
	data, _, err := profileFile(root, leaf)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return os.ErrInvalid
	}
	value := ""
	if runtime == "pi" {
		var provider, model string
		_ = json.Unmarshal(object["defaultProvider"], &provider)
		_ = json.Unmarshal(object["defaultModel"], &model)
		value = provider + "/" + model
	} else {
		var agents map[string]json.RawMessage
		if json.Unmarshal(object["agent"], &agents) == nil {
			var agent map[string]json.RawMessage
			_ = json.Unmarshal(agents["gentle-orchestrator"], &agent)
			_ = json.Unmarshal(agent["model"], &value)
		}
	}
	_, err = io.WriteString(out, "runtime="+runtime+" scope=user_config_only primary="+value+"\n")
	return err
}
