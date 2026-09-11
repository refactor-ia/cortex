package qapi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRuntimeVersionExecutor(t *testing.T) {
	if testing.Short() {
		t.Skip("local helper process coverage")
	}
	cwd := runtimeCWD(t)
	helper := buildVersionHelper(t, "success", cwd)
	capture := runVersionCommand(context.Background(), helper, cwd)
	if !capture.started || capture.startFailed || capture.timedOut || capture.waitFailed || capture.exitCode != 0 {
		t.Fatalf("success capture = %#v", capture)
	}
	if string(capture.stdout) != "pi 0.85.1\n" || len(capture.stderr) != 0 || capture.stdoutTruncated || capture.stderrTruncated {
		t.Fatalf("success output = %q / %q", capture.stdout, capture.stderr)
	}
}

func TestRuntimeVersionExecutorCapturesIndependentCaps(t *testing.T) {
	if testing.Short() {
		t.Skip("local helper process coverage")
	}
	cwd := runtimeCWD(t)
	capture := runVersionCommand(context.Background(), buildVersionHelper(t, "overflow", cwd), cwd)
	if !capture.started || capture.exitCode != 0 || !capture.stdoutTruncated || !capture.stderrTruncated {
		t.Fatalf("overflow capture = %#v", capture)
	}
	if len(capture.stdout) != maxVersionOutputBytes || len(capture.stderr) != maxVersionOutputBytes {
		t.Fatalf("capture sizes = %d / %d", len(capture.stdout), len(capture.stderr))
	}
}

func TestRuntimeVersionExecutorTerminalOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("local helper process coverage")
	}
	cwd := runtimeCWD(t)
	t.Run("launch failure", func(t *testing.T) {
		capture := runVersionCommand(context.Background(), filepath.Join(cwd, "missing"), cwd)
		if !capture.startFailed || capture.started || capture.timedOut || capture.waitFailed {
			t.Fatalf("launch capture = %#v", capture)
		}
	})
	t.Run("nonzero exit", func(t *testing.T) {
		capture := runVersionCommand(context.Background(), buildVersionHelper(t, "nonzero", cwd), cwd)
		if !capture.started || capture.startFailed || capture.exitCode != 7 || capture.waitFailed || capture.timedOut {
			t.Fatalf("nonzero capture = %#v", capture)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		original := versionProbeTimeout
		versionProbeTimeout = 20 * time.Millisecond
		t.Cleanup(func() { versionProbeTimeout = original })
		capture := runVersionCommand(context.Background(), buildVersionHelper(t, "block", cwd), cwd)
		if !capture.started || !capture.timedOut || capture.startFailed || capture.waitFailed {
			t.Fatalf("timeout capture = %#v", capture)
		}
	})
}

func TestRuntimeVersionExecutorWaitDelay(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("portable inherited-pipe coverage")
	}
	cwd := runtimeCWD(t)
	capture := runVersionCommand(context.Background(), buildVersionHelper(t, "drain", cwd), cwd)
	if !capture.started || !capture.waitFailed || capture.startFailed || capture.timedOut {
		t.Fatalf("drain capture = %#v", capture)
	}
}

func runtimeCWD(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

func buildVersionHelper(t *testing.T, mode, cwd string) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	program := fmt.Sprintf(`package main
import ("bytes"; "fmt"; "os"; "os/exec"; "time")
func main() {
 if len(os.Args) == 2 && os.Args[1] == "--child" { time.Sleep(1200*time.Millisecond); return }
 if len(os.Args) != 2 || os.Args[1] != "--version" { os.Exit(3) }
 if cwd, _ := os.Getwd(); cwd != %q { os.Exit(4) }
 for _, key := range []string{"PATH", "HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR"} { if os.Getenv(key) != "" { os.Exit(5) } }
 if os.Getenv("LC_ALL") != "C" || os.Getenv("LANG") != "C" || os.Getenv("NO_COLOR") != "1" || os.Getenv("TERM") != "dumb" { os.Exit(6) }
 switch %q {
 case "success": fmt.Print("pi 0.85.1\n")
 case "overflow": os.Stdout.Write(bytes.Repeat([]byte("o"), 65537)); os.Stderr.Write(bytes.Repeat([]byte("e"), 65537))
 case "nonzero": fmt.Fprint(os.Stderr, "failed"); os.Exit(7)
 case "block": time.Sleep(2*time.Second)
 case "drain": child := exec.Command(os.Args[0], "--child"); child.Stdout = os.Stdout; child.Stderr = os.Stderr; if child.Start() != nil { os.Exit(8) }
 }
}
`, cwd, mode)
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(directory, "version-helper")
	command := exec.Command("go", "build", "-o", helper, source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, output)
	}
	return helper
}
