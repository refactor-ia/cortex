package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDoctorOnTarget(t *testing.T, target string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var args []string
	if target != "" {
		args = []string{"doctor", "--target-dir", target}
	} else {
		args = []string{"doctor"}
	}
	code := runWithDependencies(context.Background(), args, &stdout, &stderr, nil, directoryInstallDependencies(t, canonicalHome(t)), defaultUninstallDependencies())
	return code, stdout.String(), stderr.String()
}

func doctorTargetLine(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "target=") {
			return line
		}
	}
	return ""
}

func TestDoctorDirectoryTargetStates(t *testing.T) {
	home := canonicalHome(t)

	// Absent: the path does not exist.
	code, stdout, stderr := runDoctorOnTarget(t, filepath.Join(home, "absent"))
	if stderr != "" || code != exitOK {
		t.Fatalf("absent: exit = %d stderr = %q", code, stderr)
	}
	if line := doctorTargetLine(stdout); !strings.Contains(line, "presence=absent") {
		t.Fatalf("absent line = %q", line)
	}

	// Unowned: an existing directory with no Cortex state.
	unowned := filepath.Join(home, "unowned")
	if err := os.MkdirAll(unowned, 0o700); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runDoctorOnTarget(t, unowned)
	if code != exitOK {
		t.Fatalf("unowned: exit = %d", code)
	}
	if line := doctorTargetLine(stdout); !strings.Contains(line, "presence=present") || !strings.Contains(line, "writability=writable") || !strings.Contains(line, "ownership=unowned") {
		t.Fatalf("unowned line = %q", line)
	}

	// Owned: a directory the directory target installed into.
	owned := filepath.Join(home, "owned")
	if err := os.MkdirAll(owned, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLifecycleInstall(t, home, owned)
	code, stdout, _ = runDoctorOnTarget(t, owned)
	if code != exitOK {
		t.Fatalf("owned: exit = %d", code)
	}
	if line := doctorTargetLine(stdout); !strings.Contains(line, "ownership=owned") {
		t.Fatalf("owned line = %q", line)
	}
}

func TestDoctorDirectoryTargetUnwritable(t *testing.T) {
	home := canonicalHome(t)
	unwritable := filepath.Join(home, "unwritable")
	if err := os.MkdirAll(unwritable, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o700) })
	if os.Getuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	code, stdout, _ := runDoctorOnTarget(t, unwritable)
	if line := doctorTargetLine(stdout); !strings.Contains(line, "writability=unwritable") {
		t.Fatalf("unwritable line = %q", line)
	}
	if code != exitConflict {
		t.Fatalf("unwritable exit = %d, want conflict", code)
	}
}

func TestDoctorDirectoryTargetUnreadableStateIsInvalid(t *testing.T) {
	home := canonicalHome(t)
	writeState := func(name string, data []byte) string {
		t.Helper()
		target := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Join(target, ".cortex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, ".cortex", "install-state.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return target
	}

	// An undecodable state is the existing invalid classification.
	garbage := writeState("garbage", []byte("not json"))
	garbageCode, garbageOut, garbageErr := runDoctorOnTarget(t, garbage)
	if garbageErr != "" || garbageOut != "target="+garbage+" presence=present writability=writable ownership=invalid\n" {
		t.Fatalf("garbage: exit = %d stdout = %q stderr = %q", garbageCode, garbageOut, garbageErr)
	}

	// An unreadable state exists, so it is invalid exactly like an undecodable one.
	unreadable := writeState("unreadable", []byte("{}"))
	statePath := filepath.Join(unreadable, ".cortex", "install-state.json")
	if err := os.Chmod(statePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(statePath, 0o600) })
	if os.Getuid() == 0 {
		t.Skip("root can read a mode 0000 file")
	}
	code, stdout, stderr := runDoctorOnTarget(t, unreadable)
	if want := "target=" + unreadable + " presence=present writability=writable ownership=invalid\n"; stdout != want {
		t.Fatalf("unreadable stdout = %q, want %q", stdout, want)
	}
	if stderr != "" || code != exitOK || garbageCode != exitOK {
		t.Fatalf("unreadable: exit = %d (invalid exits %d) stderr = %q", code, garbageCode, stderr)
	}
}
