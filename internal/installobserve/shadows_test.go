package installobserve

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestShadowScannerReadsFourRootsInOrderAndDetaches(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	roots := []struct {
		location shadowLocation
		path     string
		name     string
		body     string
	}{
		{globalAgents, filepath.Join(piRoot, "agents"), "global-agent.md", "global agent"},
		{globalSubagents, filepath.Join(piRoot, "subagents"), "global-subagent.md", "global subagent"},
		{projectAgents, filepath.Join(cwd, ".pi", "agents"), "project-agent.md", "project agent"},
		{projectSubagents, filepath.Join(cwd, ".pi", "subagents"), "project-subagent.md", "project subagent"},
	}
	for _, root := range roots {
		writeShadowFile(t, filepath.Join(root.path, root.name), []byte(root.body), 0o640)
	}
	got, err := scanActorRoots(piRoot, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(roots) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(roots))
	}
	for index, want := range roots {
		candidate := got[index]
		if candidate.location != want.location || candidate.basename != want.name ||
			!bytes.Equal(candidate.bytes, []byte(want.body)) || candidate.mode.Perm() != 0o640 || candidate.unsafe {
			t.Fatalf("candidate %d = %#v", index, candidate)
		}
	}
	got[0].bytes[0] ^= 1
	again, err := scanActorRoots(piRoot, cwd)
	if err != nil || !bytes.Equal(again[0].bytes, []byte(roots[0].body)) {
		t.Fatalf("scanner retained aliased bytes: %#v, %v", again, err)
	}
}

func TestShadowScannerSkipsMissingOptionalAndNestedDirectories(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	writeShadowFile(t, filepath.Join(piRoot, "agents", "nested", "ignored.md"), []byte("nested"), 0o600)
	writeShadowFile(t, filepath.Join(cwd, ".pi", "agents", "kept.txt"), []byte("text"), 0o600)
	got, err := scanActorRoots(piRoot, cwd)
	if err != nil || len(got) != 0 {
		t.Fatalf("scanActorRoots() = %#v, %v", got, err)
	}
}

func TestShadowScannerRejectsUnsafeRoots(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	missing := filepath.Join(shadowTempDir(t), "missing")
	file := filepath.Join(shadowTempDir(t), "file")
	writeShadowFile(t, file, nil, 0o600)
	link := filepath.Join(shadowTempDir(t), "link")
	if err := os.Symlink(filepath.Dir(piRoot), link); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		piRoot, cwd string
	}{
		{"relative pi root", "relative", cwd},
		{"unclean pi root", piRoot + string(filepath.Separator) + ".", cwd},
		{"missing pi root", missing, cwd},
		{"symlink pi root", link, cwd},
		{"parent symlink pi root", filepath.Join(link, filepath.Base(piRoot)), cwd},
		{"file pi root", file, cwd},
		{"relative cwd", piRoot, "relative"},
		{"unclean cwd", piRoot, cwd + string(filepath.Separator) + "."},
		{"missing cwd", piRoot, missing},
		{"symlink cwd", piRoot, link},
		{"file cwd", piRoot, file},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := scanActorRoots(test.piRoot, test.cwd); err == nil {
				t.Fatal("scanActorRoots() accepted unsafe root")
			}
		})
	}
}

func TestShadowScannerSortsBasenamesAndRejectsUnsafeComponents(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	writeShadowFile(t, filepath.Join(piRoot, "agents", "z.md"), nil, 0o600)
	writeShadowFile(t, filepath.Join(piRoot, "agents", "a.md"), nil, 0o600)
	got, err := scanActorRoots(piRoot, cwd)
	if err != nil || len(got) != 2 || got[0].basename != "a.md" || got[1].basename != "z.md" {
		t.Fatalf("scanActorRoots() = %#v, %v", got, err)
	}
	linkRoot, linkCWD := shadowTempDir(t), shadowTempDir(t)
	if err := os.Symlink("elsewhere", filepath.Join(linkRoot, "agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanActorRoots(linkRoot, linkCWD); err == nil {
		t.Fatal("scanActorRoots() accepted a symlink component")
	}
	fileRoot, fileCWD := shadowTempDir(t), shadowTempDir(t)
	writeShadowFile(t, filepath.Join(fileCWD, ".pi"), nil, 0o600)
	if _, err := scanActorRoots(fileRoot, fileCWD); err == nil {
		t.Fatal("scanActorRoots() accepted a non-directory component")
	}
}

func TestShadowScannerMarksUnsafeCandidatesWithoutReading(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	root := filepath.Join(piRoot, "agents")
	writeShadowFile(t, filepath.Join(root, "safe.md"), []byte("safe"), 0o600)
	if err := os.MkdirAll(filepath.Join(root, "directory.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("safe.md", filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	writeShadowFile(t, filepath.Join(root, "large.md"), bytes.Repeat([]byte("x"), shadowMaxFileBytes+1), 0o600)
	got, err := scanActorRoots(piRoot, cwd)
	if err != nil || len(got) != 4 {
		t.Fatalf("scanActorRoots() = %#v, %v", got, err)
	}
	for _, candidate := range got {
		if candidate.basename == "safe.md" {
			if candidate.unsafe || !bytes.Equal(candidate.bytes, []byte("safe")) {
				t.Fatalf("safe candidate = %#v", candidate)
			}
			continue
		}
		if !candidate.unsafe || len(candidate.bytes) != 0 {
			t.Fatalf("unsafe candidate = %#v", candidate)
		}
	}
}

func TestShadowScannerEnforcesEntryAndRetainedByteBounds(t *testing.T) {
	t.Run("entry overflow", func(t *testing.T) {
		piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
		root := filepath.Join(piRoot, "agents")
		for index := 0; index <= shadowMaxEntries; index++ {
			writeShadowFile(t, filepath.Join(root, "entry-"+strconv.Itoa(index)+".txt"), nil, 0o600)
		}
		if _, err := scanActorRoots(piRoot, cwd); err == nil {
			t.Fatal("scanActorRoots() accepted too many entries")
		}
	})
	t.Run("retained byte overflow", func(t *testing.T) {
		piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
		root := filepath.Join(piRoot, "agents")
		for index := 0; index <= shadowMaxRetainedBytes/shadowMaxFileBytes; index++ {
			writeShadowFile(t, filepath.Join(root, "file-"+strconv.Itoa(index)+".md"), bytes.Repeat([]byte("x"), shadowMaxFileBytes), 0o600)
		}
		if _, err := scanActorRoots(piRoot, cwd); err == nil {
			t.Fatal("scanActorRoots() accepted too many retained bytes")
		}
	})
}

func TestShadowScannerAcceptsExactFileLimit(t *testing.T) {
	piRoot, cwd := shadowTempDir(t), shadowTempDir(t)
	body := bytes.Repeat([]byte("x"), shadowMaxFileBytes)
	writeShadowFile(t, filepath.Join(piRoot, "agents", "exact.md"), body, 0o600)

	got, err := scanActorRoots(piRoot, cwd)
	if err != nil || len(got) != 1 || got[0].unsafe || !bytes.Equal(got[0].bytes, body) {
		t.Fatalf("scanActorRoots() = %#v, %v", got, err)
	}
}

func writeShadowFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func shadowTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
