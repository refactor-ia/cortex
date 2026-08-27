package filetxn_test

import filetxn "github.com/refactor-ia/cortex/internal/filetxn"

var _ = []interface{}{filetxn.ApplyOperationsWithDirectories, filetxn.Directory{Path: "dir", Mode: 0o700}, filetxn.Operation{Create: &filetxn.Create{Path: "dir/file", Mode: 0o600}}}
