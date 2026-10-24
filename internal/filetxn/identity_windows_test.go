//go:build windows

package filetxn

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, output)
	}
}

// pinnedLstat returns an Lstat result whose lazily loaded Windows identity is
// already fixed, the way callers hold it after an os.SameFile comparison.
func pinnedLstat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	must(t, err)
	if !os.SameFile(info, info) {
		t.Fatalf("identity is unavailable: %s", path)
	}
	return info
}

func TestExtendedLengthPath(t *testing.T) {
	long := `C:\` + strings.Repeat(`d\`, 130) + "leaf"
	unc := `\\server\share\` + strings.Repeat(`d\`, 130) + "leaf"
	device := `\\.\` + long[3:]
	// at247 and at248 sit on the os threshold: shorter than 248 stays as is.
	at247 := `C:\` + strings.Repeat("a", 244)
	at248 := at247 + "a"
	if len(at247) != 247 || len(at248) != 248 {
		t.Fatalf("boundary fixtures are %d and %d characters", len(at247), len(at248))
	}
	for _, test := range []struct{ name, path, want string }{
		{"short absolute", `C:\short\dir`, `C:\short\dir`},
		{"247 characters", at247, at247},
		{"248 characters", at248, `\\?\` + at248},
		{"long device path", device, device},
		{"long absolute", long, `\\?\` + long},
		{"long UNC", unc, `\\?\UNC\` + unc[2:]},
		{"already extended", `\\?\` + long, `\\?\` + long},
		{"long relative", strings.Repeat(`d\`, 130) + "leaf", strings.Repeat(`d\`, 130) + "leaf"},
		{"long unclean", long + `\.\x`, long + `\.\x`},
	} {
		if got := extendedLengthPath(test.path); got != test.want {
			t.Errorf("%s: extendedLengthPath(%q) = %q, want %q", test.name, test.path, got, test.want)
		}
	}
}

// TestRestartDirectoryMatchesAcceptedWindows drives the production restart
// check with the resolved target it receives in rollback and classification,
// so the Windows wiring of the path argument is exercised, not only the helper.
func TestRestartDirectoryMatchesAcceptedWindows(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "made")
	must(t, os.Mkdir(target, 0o700))
	info := pinnedLstat(t, target)
	device, inode, ok := directoryIdentity(target, info)
	if !ok {
		t.Fatal("directory identity is unavailable")
	}
	accepted := AcceptedDirectory{Path: "made", Device: device, Inode: inode, Mode: uint32(info.Mode().Perm())}
	if !restartDirectoryMatchesAccepted(target, info, accepted) {
		t.Fatal("accepted directory does not match its own identity")
	}
	if restartDirectoryMatchesAccepted("made", info, accepted) {
		t.Fatal("root-relative path matched; the check must use the resolved target")
	}
	replaceDirectoryWithDistinctIdentity(t, root, "made")
	if restartDirectoryMatchesAccepted(target, pinnedLstat(t, target), accepted) {
		t.Fatal("replacement directory matched the accepted identity")
	}
}

// TestDirectoryIdentityOpensLongPathsExtended proves the identity read uses the
// extended-length form even where the runtime long-path opt-in would make a
// plain long path work: a trailing dot survives only in the \\?\ form, since
// Win32 path normalization strips it from a plain path.
func TestDirectoryIdentityOpensLongPathsExtended(t *testing.T) {
	parent := t.TempDir()
	for len(parent) <= 260 {
		parent = filepath.Join(parent, strings.Repeat("d", 40))
	}
	must(t, os.MkdirAll(parent, 0o700))
	path := filepath.Join(parent, "dotted.")
	must(t, os.Mkdir(`\\?\`+path, 0o700))
	t.Cleanup(func() {
		if err := os.Remove(`\\?\` + path); err != nil {
			t.Errorf("remove trailing-dot directory: %v", err)
		}
	})
	if _, err := os.Lstat(filepath.Join(parent, "dotted")); !os.IsNotExist(err) {
		t.Fatalf("fixture is ambiguous: a plain-normalized twin exists: %v", err)
	}
	if _, _, ok := directoryIdentity(path, pinnedLstat(t, `\\?\`+path)); !ok {
		t.Fatal("long trailing-dot directory has no identity; the read did not use the extended-length form")
	}
}

func TestDirectoryIdentityWindowsRefusals(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file.txt")
		writeFile(t, path, []byte("x"), 0o600)
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); ok {
			t.Fatal("regular file reported a directory identity")
		}
	})
	t.Run("junction", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "real"), 0o700))
		makeJunction(t, filepath.Join(root, "link"), filepath.Join(root, "real"))
		info, err := os.Lstat(filepath.Join(root, "link"))
		must(t, err)
		if _, _, ok := directoryIdentity(filepath.Join(root, "link"), info); ok {
			t.Fatal("junction reported a directory identity")
		}
	})
	t.Run("directory replaced by a junction after lstat", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		must(t, os.Mkdir(filepath.Join(root, "elsewhere"), 0o700))
		stale := pinnedLstat(t, filepath.Join(root, "made"))
		must(t, os.Rename(filepath.Join(root, "made"), filepath.Join(root, "retired")))
		makeJunction(t, filepath.Join(root, "made"), filepath.Join(root, "elsewhere"))
		if _, _, ok := directoryIdentity(filepath.Join(root, "made"), stale); ok {
			t.Fatal("junction replacement reported the stale directory identity")
		}
	})
	t.Run("directory replaced by another directory after lstat", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "made")
		must(t, os.Mkdir(path, 0o700))
		stale := pinnedLstat(t, path)
		replaceDirectoryWithDistinctIdentity(t, root, "made")
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); !ok {
			t.Fatal("fresh lstat of the replacement has no identity")
		}
		if _, _, ok := directoryIdentity(path, stale); ok {
			t.Fatal("stale lstat reported an identity for the replacement")
		}
	})
	// directoryIdentity opens every long path through the \\?\ prefix, so this
	// holds without the runtime long-path opt-in or the LongPathsEnabled value.
	t.Run("path longer than MAX_PATH", func(t *testing.T) {
		path := t.TempDir()
		for len(path) <= 300 {
			path = filepath.Join(path, strings.Repeat("d", 40))
		}
		must(t, os.MkdirAll(path, 0o700))
		if _, _, ok := directoryIdentity(path, pinnedLstat(t, path)); !ok {
			t.Fatalf("long path (%d chars) has no identity", len(path))
		}
	})
}
