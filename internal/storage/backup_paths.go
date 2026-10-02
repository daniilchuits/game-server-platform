package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// inspectDirectories rejects links at every component, including Windows
// junctions. Looking only at the last directory would miss linked ancestors.
func inspectDirectories(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect directory %s: %w", current, err)
		}
		if isLink(info) {
			return fmt.Errorf("symbolic links or junctions are not allowed: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("path is not a directory: %s", current)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func ensureBackupDirectory(root, backups string) error {
	if err := inspectDirectories(root); err != nil {
		return err
	}
	if err := os.Mkdir(backups, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create backups directory: %w", err)
	}
	return inspectDirectories(backups)
}

func validateWorld(ctx context.Context, root, world string) error {
	relative, err := filepath.Rel(root, world)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return fmt.Errorf("world must be inside the server directory: %s", world)
	}
	first, _, _ := strings.Cut(relative, string(filepath.Separator))
	if strings.EqualFold(first, "backups") {
		return fmt.Errorf("world cannot be inside backups: %s", world)
	}
	return validateTree(ctx, world)
}

func validateTree(ctx context.Context, root string) error {
	if err := inspectDirectories(root); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if isLink(info) {
			return fmt.Errorf("symbolic links or junctions are not allowed in world: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported world entry: %s", path)
		}
		return nil
	})
}
