//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestQAConfigPtyHelper is the child process of
// TestNonConfiguratorCommandOnSilentPtyIsFast; it is inert in a normal run.
func TestQAConfigPtyHelper(t *testing.T) {
	if os.Getenv("CORTEX_PTY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	os.Exit(Run(context.Background(), []string{"--help"}, os.Stdout, os.Stderr, nil))
}

// S6: linking the TUI library must not make other commands query the
// terminal. The child's stdin/stdout/stderr are a pty nobody answers, so a
// package init that sends OSC 11 / DSR would stall and leak escape bytes.
func TestNonConfiguratorCommandOnSilentPtyIsFast(t *testing.T) {
	master, slave := openTestPty(t)
	defer master.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestQAConfigPtyHelper$")
	command.Env = append(os.Environ(), "CORTEX_PTY_HELPER=1")
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	started := time.Now()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	output := make(chan []byte, 1)
	go func() {
		var buffer bytes.Buffer
		_, _ = io.Copy(&buffer, master) // ends with EIO once the child exits
		output <- buffer.Bytes()
	}()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("cortex --help on a silent pty took more than 1s")
	}
	elapsed := time.Since(started)
	text := string(<-output)
	if strings.Contains(text, "\x1b]11;?") || strings.Contains(text, "\x1b[6n") || !strings.Contains(text, "usage: cortex") {
		t.Fatalf("elapsed=%s output=%q", elapsed, text)
	}
}
