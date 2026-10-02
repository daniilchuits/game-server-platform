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

type RestoreStore struct {
	copyWorld func(context.Context, string, string) error
	rename    func(string, string) error
}

func (RestoreStore) PrepareRestore(
	ctx context.Context,
	serverDirectory string,
	version string,
	backupName string,
) (domain.RestorePlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.RestorePlan{}, err
	}
	root, err := filepath.Abs(serverDirectory)
	if err != nil {
		return domain.RestorePlan{}, err
	}
	if err := validateBackupName(backupName); err != nil {
		return domain.RestorePlan{}, err
	}
	target, err := (ServerFiles{}).WorldDirectory(root)
	if err != nil {
		return domain.RestorePlan{}, err
	}
	if err := validateWorld(ctx, root, target); err != nil {
		return domain.RestorePlan{}, err
	}

	backupDirectory := filepath.Join(root, domain.Backups, backupName)
	source := filepath.Join(backupDirectory, "world")
	if err := validateSnapshot(ctx, root, backupDirectory, source, version); err != nil {
		return domain.RestorePlan{}, err
	}
	return domain.RestorePlan{
		ServerDirectory: root,
		BackupName:      backupName,
		BackupDirectory: backupDirectory,
		SourceWorld:     source,
		TargetWorld:     target,
		Version:         version,
	}, nil
}

func (store RestoreStore) InstallRestore(
	ctx context.Context,
	plan domain.RestorePlan,
) (domain.RestoreTransaction, error) {
	if err := validateRestorePlan(ctx, plan); err != nil {
		return nil, err
	}
	operationDirectory, err := os.MkdirTemp(plan.ServerDirectory, ".restore-")
	if err != nil {
		return nil, fmt.Errorf("create restore staging directory: %w", err)
	}
	stagedWorld := filepath.Join(operationDirectory, "world")
	rollbackWorld := filepath.Join(operationDirectory, "previous-world")
	cleanup := func(failure error) error {
		return errors.Join(failure, removeRestoreDirectory(operationDirectory))
	}

	copyWorld := store.copyWorld
	if copyWorld == nil {
		copyWorld = copyTree
	}
	if err := copyWorld(ctx, plan.SourceWorld, stagedWorld); err != nil {
		return nil, cleanup(fmt.Errorf("stage restored world: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, cleanup(err)
	}
	rename := store.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(plan.TargetWorld, rollbackWorld); err != nil {
		return nil, cleanup(fmt.Errorf("preserve current world for rollback: %w", err))
	}

	transaction := &restoreTransaction{
		operationDirectory: operationDirectory,
		targetWorld:        plan.TargetWorld,
		rollbackWorld:      rollbackWorld,
		rename:             rename,
	}
	if err := rename(stagedWorld, plan.TargetWorld); err != nil {
		return transaction, fmt.Errorf("install restored world: %w", err)
	}
	return transaction, nil
}

type restoreTransaction struct {
	operationDirectory string
	targetWorld        string
	rollbackWorld      string
	rename             func(string, string) error
}

func (transaction *restoreTransaction) Commit() error {
	if _, err := os.Lstat(transaction.targetWorld); err != nil {
		return fmt.Errorf("inspect restored world before commit: %w", err)
	}
	if err := removeRestoreDirectory(transaction.operationDirectory); err != nil {
		return fmt.Errorf("remove restore rollback data: %w", err)
	}
	return nil
}

func (transaction *restoreTransaction) Rollback() (bool, error) {
	failedWorld := filepath.Join(transaction.operationDirectory, "failed-world")
	if _, err := os.Lstat(transaction.targetWorld); err == nil {
		if err := transaction.rename(transaction.targetWorld, failedWorld); err != nil {
			return false, fmt.Errorf("preserve failed restored world: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect failed restored world: %w", err)
	}
	if err := transaction.rename(transaction.rollbackWorld, transaction.targetWorld); err != nil {
		return false, fmt.Errorf("restore original world: %w", err)
	}
	if err := removeRestoreDirectory(transaction.operationDirectory); err != nil {
		return true, fmt.Errorf("remove failed restore data: %w", err)
	}
	return true, nil
}

func validateRestorePlan(ctx context.Context, plan domain.RestorePlan) error {
	root, err := filepath.Abs(plan.ServerDirectory)
	if err != nil {
		return err
	}
	if root != plan.ServerDirectory {
		return errors.New("restore server directory must be absolute")
	}
	if err := validateBackupName(plan.BackupName); err != nil {
		return err
	}
	expectedBackup := filepath.Join(root, domain.Backups, plan.BackupName)
	if filepath.Clean(plan.BackupDirectory) != expectedBackup ||
		filepath.Clean(plan.SourceWorld) != filepath.Join(expectedBackup, "world") {
		return errors.New("restore source is outside the selected backup")
	}
	expectedTarget, err := (ServerFiles{}).WorldDirectory(root)
	if err != nil {
		return err
	}
	if filepath.Clean(plan.TargetWorld) != expectedTarget {
		return errors.New("restore target does not match level-name")
	}
	if err := validateWorld(ctx, root, plan.TargetWorld); err != nil {
		return err
	}
	return validateSnapshot(ctx, root, plan.BackupDirectory, plan.SourceWorld, plan.Version)
}

func validateSnapshot(
	ctx context.Context,
	root string,
	backupDirectory string,
	sourceWorld string,
	version string,
) error {
	backups := filepath.Join(root, domain.Backups)
	if filepath.Clean(filepath.Dir(backupDirectory)) != backups {
		return errors.New("selected backup must be directly inside the backups directory")
	}
	if err := inspectDirectories(backupDirectory); err != nil {
		return err
	}
	metadataPath := filepath.Join(backupDirectory, "backup.json")
	metadataFile, err := os.Lstat(metadataPath)
	if err != nil {
		return fmt.Errorf("inspect backup metadata: %w", err)
	}
	if !metadataFile.Mode().IsRegular() || isLink(metadataFile) {
		return errors.New("backup metadata must be a regular file")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return fmt.Errorf("read backup metadata: %w", err)
	}
	var metadata domain.BackupMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decode backup metadata: %w", err)
	}
	if metadata.Game != "minecraft" {
		return fmt.Errorf("backup game is %q, expected minecraft", metadata.Game)
	}
	if metadata.Version != version {
		return fmt.Errorf("backup version is %q, expected %s", metadata.Version, version)
	}
	if strings.TrimSpace(metadata.World) == "" {
		return errors.New("backup metadata has no world name")
	}
	metadataWorld := filepath.Clean(filepath.FromSlash(metadata.World))
	if metadataWorld == "." || !filepath.IsLocal(metadataWorld) {
		return errors.New("backup metadata has an invalid world name")
	}
	if _, err := time.Parse(time.RFC3339, metadata.CreatedAt); err != nil {
		return fmt.Errorf("backup metadata has invalid creation time: %w", err)
	}
	return validateTree(ctx, sourceWorld)
}

func validateBackupName(name string) error {
	if name == "" || name == "." || filepath.Clean(name) != name || filepath.Base(name) != name || !filepath.IsLocal(name) {
		return fmt.Errorf("invalid backup name %q", name)
	}
	return nil
}

func removeRestoreDirectory(path string) error {
	if filepath.Base(path) == path || !strings.HasPrefix(filepath.Base(path), ".restore-") {
		return fmt.Errorf("refusing to remove unowned restore directory: %s", path)
	}
	return os.RemoveAll(path)
}
