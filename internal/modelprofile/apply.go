package modelprofile

// ApplySaved conditionally applies a saved backup's after images. It retains the
// backup unchanged so it remains valid rollback evidence after a successful apply.
func ApplySaved(roots RuntimeRoots, backup Backup) error {
	entries := cloneEntries(backup.entries)
	for i := range entries {
		entries[i].Before, entries[i].After = entries[i].After, entries[i].Before
	}
	return Restore(roots, Backup{entries: entries})
}

// BackupTargets returns the selected config leaves without exposing backup images.
func BackupTargets(backup Backup) []Target {
	targets := make([]Target, len(backup.entries))
	for i, entry := range backup.entries {
		targets[i] = entry.Target
	}
	return targets
}
