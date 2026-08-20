package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const StateOutboxPathConflictCode = "state_outbox_path_conflict"

func validateDistinctStateAndOutboxPaths(statePath string, outboxPath string) error {
	stateCanonical, err := canonicalFuturePath(statePath)
	if err != nil {
		return fmt.Errorf("canonicalize paths.state_file: %w", err)
	}
	outboxCanonical, err := canonicalFuturePath(outboxPath)
	if err != nil {
		return fmt.Errorf("canonicalize paths.outbox_file: %w", err)
	}
	conflict := stateCanonical == outboxCanonical
	stateInfo, stateErr := os.Stat(stateCanonical)
	outboxInfo, outboxErr := os.Stat(outboxCanonical)
	if stateErr == nil && outboxErr == nil && os.SameFile(stateInfo, outboxInfo) {
		conflict = true
	}
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return fmt.Errorf("stat paths.state_file: %w", stateErr)
	}
	if outboxErr != nil && !errors.Is(outboxErr, os.ErrNotExist) {
		return fmt.Errorf("stat paths.outbox_file: %w", outboxErr)
	}
	if conflict {
		return fmt.Errorf("%s: paths.state_file and paths.outbox_file must resolve to different files", StateOutboxPathConflictCode)
	}
	return nil
}

// canonicalFuturePath resolves aliases even when the final file does not yet
// exist: it resolves the deepest existing ancestor and appends the remaining
// cleaned components. This makes validation stable before either store opens.
func canonicalFuturePath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	current := absolute
	remaining := make([]string, 0)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(remaining) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, remaining[index])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", absolute)
		}
		remaining = append(remaining, filepath.Base(current))
		current = parent
	}
}
