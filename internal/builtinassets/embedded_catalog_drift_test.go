package builtinassets

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// repositoryCatalogRoot is the repository catalog the embedded tree must mirror.
const repositoryCatalogRoot = "../../catalog"

// TestEmbeddedCatalogMatchesRepositoryCatalog fails when the embedded catalog
// and the repository catalog stop being byte-for-byte identical.
func TestEmbeddedCatalogMatchesRepositoryCatalog(t *testing.T) {
	embedded := readTree(t, embeddedCatalogFS(t))
	repository := readTree(t, os.DirFS(repositoryCatalogRoot))
	for _, path := range sortedPaths(embedded, repository) {
		embeddedContent, inEmbedded := embedded[path]
		repositoryContent, inRepository := repository[path]
		switch {
		case !inRepository:
			t.Errorf("embedded catalog has %q, repository catalog does not", path)
		case !inEmbedded:
			t.Errorf("repository catalog has %q, embedded catalog does not", path)
		case !bytes.Equal(embeddedContent, repositoryContent):
			t.Errorf("embedded and repository catalog disagree on the content of %q", path)
		}
	}
}

func embeddedCatalogFS(t *testing.T) fs.FS {
	t.Helper()
	catalogFS, err := fs.Sub(assets, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	return catalogFS
}

func readTree(t *testing.T, tree fs.FS) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, readErr := fs.ReadFile(tree, path)
		if readErr != nil {
			return readErr
		}
		files[filepath.ToSlash(path)] = content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func sortedPaths(trees ...map[string][]byte) []string {
	unique := map[string]struct{}{}
	for _, tree := range trees {
		for path := range tree {
			unique[path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
