package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"game-server-platform/internal/domain"
)

type BackupStore struct {
	copyWorld func(context.Context, string, string) error
}

func (BackupStore) Prepare(ctx context.Context, serverDirectory, version string, now time.Time) (domain.BackupPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.BackupPlan{}, err
	}
	root, err := filepath.Abs(serverDirectory)
	if err != nil {
		return domain.BackupPlan{}, err
	}
	world, err := (ServerFiles{}).WorldDirectory(root)
	if err != nil {
		return domain.BackupPlan{}, err
	}
	if err := validateWorld(ctx, root, world); err != nil {
		return domain.BackupPlan{}, err
	}
	backups := filepath.Join(root, domain.Backups)
	if err := ensureBackupDirectory(root, backups); err != nil {
		return domain.BackupPlan{}, err
	}

	// A probe catches unwritable destinations before interrupting gameplay.
	probe, err := os.CreateTemp(backups, ".write-check-")
	if err != nil {
		return domain.BackupPlan{}, fmt.Errorf("check backup destination: %w", err)
	}
	if err := errors.Join(probe.Close(), os.Remove(probe.Name())); err != nil {
		return domain.BackupPlan{}, fmt.Errorf("clean backup destination probe: %w", err)
	}
	base := now.UTC().Format("2006-01-02_15-04-05Z")
	destination := filepath.Join(backups, base)
	for suffix := 1; ; suffix++ {
		_, err := os.Lstat(destination)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return domain.BackupPlan{}, fmt.Errorf("check backup destination: %w", err)
		}
		destination = filepath.Join(backups, fmt.Sprintf("%s_%d", base, suffix))
	}
	name, err := filepath.Rel(root, world)
	if err != nil {
		return domain.BackupPlan{}, err
	}
	return domain.BackupPlan{
		ServerDirectory: root, WorldDirectory: world, WorldName: filepath.ToSlash(name),
		Destination: destination, CreatedAt: now.UTC().Format(time.RFC3339), Version: version,
	}, nil
}

func (store BackupStore) Create(ctx context.Context, plan domain.BackupPlan, message string) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateWorld(ctx, plan.ServerDirectory, plan.WorldDirectory); err != nil {
		return err
	}
	backups := filepath.Join(plan.ServerDirectory, domain.Backups)
	if err := ensureBackupDirectory(plan.ServerDirectory, backups); err != nil {
		return err
	}
	if filepath.Clean(filepath.Dir(plan.Destination)) != backups {
		return fmt.Errorf("backup destination must be directly inside %s", backups)
	}
	if err := destinationAvailable(plan.Destination); err != nil {
		return err
	}

	// Each operation owns only this unique temporary directory. Older incomplete
	// snapshots may contain recoverable data and must never be removed here.
	temporary, err := os.MkdirTemp(backups, ".incomplete-")
	if err != nil {
		return fmt.Errorf("create temporary backup: %w", err)
	}
	defer func() {
		if temporary == "" {
			return
		}
		if err := os.RemoveAll(temporary); err != nil {
			result = errors.Join(result, fmt.Errorf("remove incomplete backup %s: %w", temporary, err))
		}
	}()
	copyWorld := store.copyWorld
	if copyWorld == nil {
		copyWorld = copyTree
	}
	if err := copyWorld(ctx, plan.WorldDirectory, filepath.Join(temporary, "world")); err != nil {
		return fmt.Errorf("copy world: %w", err)
	}
	metadata := domain.BackupMetadata{Game: "minecraft", Version: plan.Version, World: plan.WorldName, CreatedAt: plan.CreatedAt}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode backup metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(temporary, "backup.json"), append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write backup metadata: %w", err)
	}
	if strings.TrimSpace(message) != "" {
		if err := os.WriteFile(filepath.Join(temporary, "backup_message.txt"), []byte(message), 0644); err != nil {
			return fmt.Errorf("write backup message: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := destinationAvailable(plan.Destination); err != nil {
		return err
	}
	// Publish only after every file is closed and the destination is checked.
	if err := os.Rename(temporary, plan.Destination); err != nil {
		return fmt.Errorf("publish backup: %w", err)
	}
	temporary = ""
	return nil
}

func destinationAvailable(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	return fmt.Errorf("backup destination already exists: %s", path)
}
