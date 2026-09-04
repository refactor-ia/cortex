package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const subscriptionAuthMaxBytes = 1 << 20

func copySubscriptionAuth(source, target string) error {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return realSmokeError()
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > subscriptionAuthMaxBytes {
		return realSmokeError()
	}
	contents, err := os.ReadFile(source)
	if err != nil || len(contents) == 0 || len(contents) > subscriptionAuthMaxBytes {
		return realSmokeError()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return realSmokeError()
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return realSmokeError()
	}
	writeErr := error(nil)
	if written, err := file.Write(contents); err != nil || written != len(contents) {
		writeErr = realSmokeError()
	}
	if writeErr == nil && file.Sync() != nil {
		writeErr = realSmokeError()
	}
	if file.Close() != nil || writeErr != nil {
		return realSmokeError()
	}
	return nil
}

func subscriptionAuthSourceAfterGate(authorization, required string, source func() string) (string, bool) {
	if !realSmokeAuthorized(authorization, required) {
		return "", false
	}
	return source(), true
}

func piSubscriptionAuthTarget(home string) string {
	return filepath.Join(home, ".pi", "agent", "auth.json")
}

func opencodeSubscriptionAuthTarget(home string) string {
	return filepath.Join(home, ".local", "share", "opencode", "auth.json")
}

func TestCopySubscriptionAuth(t *testing.T) {
	t.Run("copies opaque bytes with isolated permissions", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source")
		contents := []byte("opaque-subscription-auth")
		if err := os.WriteFile(source, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "home", ".pi", "agent", "auth.json")
		if err := copySubscriptionAuth(source, target); err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(copied, contents) {
			t.Fatalf("copied = %q, %v", copied, err)
		}
		home := filepath.Dir(filepath.Dir(filepath.Dir(target)))
		for path := filepath.Dir(target); ; path = filepath.Dir(path) {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("parent mode = %v, %v", info.Mode(), err)
			}
			if path == home {
				break
			}
		}
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("target mode = %v, %v", info.Mode(), err)
		}
	})

	t.Run("rejects invalid sources without disclosure", func(t *testing.T) {
		directory := t.TempDir()
		contents := "opaque-source-bytes"
		valid := filepath.Join(directory, "valid")
		if err := os.WriteFile(valid, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		empty := filepath.Join(directory, "empty")
		if err := os.WriteFile(empty, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		oversize := filepath.Join(directory, "oversize")
		if err := os.WriteFile(oversize, make([]byte, subscriptionAuthMaxBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "link")
		if err := os.Symlink(valid, link); err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{filepath.Join(directory, "missing"), empty, oversize, link, directory} {
			err := copySubscriptionAuth(source, filepath.Join(t.TempDir(), "auth.json"))
			if err == nil || err.Error() != realSmokeError().Error() || strings.Contains(err.Error(), source) || strings.Contains(err.Error(), contents) {
				t.Fatalf("error = %v", err)
			}
		}
	})

	t.Run("rejects relative and unclean absolute sources before creating the target", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(source, []byte("opaque-source-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(filepath.Dir(source))
		relative := filepath.Base(source)
		removable := filepath.Join(filepath.Dir(source), "removable")
		if err := os.Mkdir(removable, 0o700); err != nil {
			t.Fatal(err)
		}
		unclean := removable + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(source)
		for _, tc := range []struct {
			name, source string
		}{
			{name: "relative", source: relative},
			{name: "unclean absolute", source: unclean},
		} {
			t.Run(tc.name, func(t *testing.T) {
				target := filepath.Join(t.TempDir(), "isolated", "auth.json")
				err := copySubscriptionAuth(tc.source, target)
				if err == nil || err.Error() != realSmokeError().Error() {
					t.Fatalf("error = %v", err)
				}
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("target exists or stat failed: %v", err)
				}
			})
		}
	})

	t.Run("rejects an existing target", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source")
		target := filepath.Join(t.TempDir(), "auth.json")
		if err := os.WriteFile(source, []byte("opaque-source-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := copySubscriptionAuth(source, target); err == nil || err.Error() != realSmokeError().Error() {
			t.Fatalf("error = %v", err)
		}
		copied, err := os.ReadFile(target)
		if err != nil || string(copied) != "existing" {
			t.Fatalf("target = %q, %v", copied, err)
		}
	})

	t.Run("cleanup removes the isolated copy", func(t *testing.T) {
		root, source := t.TempDir(), filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(source, []byte("opaque-source-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := copySubscriptionAuth(source, piSubscriptionAuthTarget(root)); err != nil {
			t.Fatal(err)
		}
		if err := cleanupPiRealSmoke(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("root remains: %v", err)
		}
	})
}

func TestSubscriptionSmokeConfiguration(t *testing.T) {
	t.Run("source lookup follows an exact provider gate", func(t *testing.T) {
		calls := 0
		lookup := func() string { calls++; return "source" }
		if source, authorized := subscriptionAuthSourceAfterGate("", piRealSmokeAuthorization, lookup); authorized || source != "" || calls != 0 {
			t.Fatalf("unauthorized result = %q, %t, calls=%d", source, authorized, calls)
		}
		if source, authorized := subscriptionAuthSourceAfterGate(piRealSmokeAuthorization, piRealSmokeAuthorization, lookup); !authorized || source != "source" || calls != 1 {
			t.Fatalf("authorized result = %q, %t, calls=%d", source, authorized, calls)
		}
	})

	home := filepath.Join(t.TempDir(), "isolated")
	for _, tc := range []struct{ target, want string }{
		{piSubscriptionAuthTarget(home), filepath.Join(home, ".pi", "agent", "auth.json")},
		{opencodeSubscriptionAuthTarget(home), filepath.Join(home, ".local", "share", "opencode", "auth.json")},
	} {
		if tc.target != tc.want || !strings.HasPrefix(tc.target, home+string(filepath.Separator)) {
			t.Fatalf("target = %q, want %q", tc.target, tc.want)
		}
	}
	for _, secret := range []string{"ANTHROPIC_API_KEY=synthetic", "OPENAI_API_KEY=synthetic", "CLAUDE_CODE_OAUTH_TOKEN=synthetic"} {
		if strings.Contains(strings.Join(piSmokeEnvironment(home), "\x00"), secret) || strings.Contains(strings.Join(opencodeSmokeEnvironment(home), "\x00"), secret) || strings.Contains(strings.Join(claudeSmokeEnvironment(home), "\x00"), secret) {
			t.Fatalf("environment contains %q", secret)
		}
	}
	if !strings.Contains(strings.Join(opencodeSmokeEnvironment(home), "\x00"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share")) {
		t.Fatal("OpenCode environment lacks isolated data home")
	}
	if strings.Contains(claudeSmokeEvidence(strings.Repeat("a", 40), "1", "snapshot", []byte("marker"), 1), "auth=subscription_keychain") == false {
		t.Fatal("Claude evidence lacks Keychain auth attribution")
	}
	for _, evidence := range []string{
		piSmokeEvidence(strings.Repeat("a", 40), "1", "snapshot", []byte("marker"), 1),
		opencodeSmokeEvidence(strings.Repeat("a", 40), "1", "snapshot", []byte("marker"), 1),
	} {
		if !strings.Contains(evidence, "auth=subscription_copy") {
			t.Fatalf("evidence lacks subscription copy attribution: %q", evidence)
		}
	}
}
