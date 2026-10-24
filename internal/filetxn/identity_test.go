//go:build unix || windows

package filetxn

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func lstatDirectoryIdentity(t *testing.T, path string) (uint64, uint64) {
	t.Helper()
	info, err := os.Lstat(path)
	must(t, err)
	device, inode, ok := directoryIdentity(path, info)
	if !ok {
		t.Fatalf("directory identity is unavailable: %s", path)
	}
	return device, inode
}

func TestDirectoryIdentity(t *testing.T) {
	t.Run("stable for the same directory", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		device, inode := lstatDirectoryIdentity(t, filepath.Join(root, "made"))
		againDevice, againInode := lstatDirectoryIdentity(t, filepath.Join(root, "made"))
		if device != againDevice || inode != againInode {
			t.Fatalf("identity changed: %d/%d then %d/%d", device, inode, againDevice, againInode)
		}
	})
	t.Run("distinct for sibling directories", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "left"), 0o700))
		must(t, os.Mkdir(filepath.Join(root, "right"), 0o700))
		leftDevice, leftInode := lstatDirectoryIdentity(t, filepath.Join(root, "left"))
		rightDevice, rightInode := lstatDirectoryIdentity(t, filepath.Join(root, "right"))
		if leftDevice != rightDevice {
			t.Fatalf("siblings report different devices: %d and %d", leftDevice, rightDevice)
		}
		if leftInode == rightInode {
			t.Fatalf("siblings share inode %d", leftInode)
		}
	})
	t.Run("distinct after replacement", func(t *testing.T) {
		root := t.TempDir()
		must(t, os.Mkdir(filepath.Join(root, "made"), 0o700))
		_, before := lstatDirectoryIdentity(t, filepath.Join(root, "made"))
		replaceDirectoryWithDistinctIdentity(t, root, "made")
		_, after := lstatDirectoryIdentity(t, filepath.Join(root, "made"))
		if before == after {
			t.Fatalf("replacement kept inode %d", before)
		}
	})
	t.Run("stale lstat never yields the replacement identity", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "made")
		must(t, os.Mkdir(path, 0o700))
		_, original := lstatDirectoryIdentity(t, path)
		stale, err := os.Lstat(path)
		must(t, err)
		// Pin the lazily loaded Windows identity, as callers do with os.SameFile.
		if !os.SameFile(stale, stale) {
			t.Fatal("stale directory identity is unavailable")
		}
		replaceDirectoryWithDistinctIdentity(t, root, "made")
		// A fresh Lstat still resolves, so a refusal below can only come from
		// the stale result, not from an unreadable path.
		_, current := lstatDirectoryIdentity(t, path)
		_, inode, ok := directoryIdentity(path, stale)
		if runtime.GOOS == "windows" {
			// The identity is read from the path, which no longer names stale.
			if ok {
				t.Fatalf("stale lstat reported identity %d, want refusal", inode)
			}
			return
		}
		// On unix the identity is part of the Lstat result itself.
		if !ok || inode != original || inode == current {
			t.Fatalf("stale lstat identity = %d, %v; want original %d, distinct from %d", inode, ok, original, current)
		}
	})
}
