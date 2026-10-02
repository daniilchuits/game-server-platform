package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"game-server-platform/internal/domain"
)

func TestRestoreStoreUsesConfiguredWorldAndSupportsRollbackAndCommit(t *testing.T) {
	root, backupName, target := restoreFixture(t, "maps/custom")
	store := RestoreStore{}
	plan, err := store.PrepareRestore(context.Background(), root, "1.20.4", backupName)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetWorld != target {
		t.Fatalf("target = %q, want %q", plan.TargetWorld, target)
	}

	transaction, err := store.InstallRestore(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	assertWorldFile(t, target, "new")
	assertWorldFile(t, filepath.Join(root, domain.Backups, backupName, "world"), "new")
	if restored, err := transaction.Rollback(); err != nil || !restored {
		t.Fatal(err)
	}
	assertWorldFile(t, target, "old")
	assertNoRestoreDirectories(t, root)

	transaction, err = store.InstallRestore(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	assertWorldFile(t, target, "new")
	assertNoRestoreDirectories(t, root)
}

func TestRestoreStoreCopyFailureLeavesLiveWorldUntouched(t *testing.T) {
	root, backupName, target := restoreFixture(t, "world")
	failure := errors.New("copy failed")
	store := RestoreStore{copyWorld: func(_ context.Context, _, destination string) error {
		if err := os.Mkdir(destination, 0755); err != nil {
			return err
		}
		return failure
	}}
	plan, err := store.PrepareRestore(context.Background(), root, "1.20.4", backupName)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.InstallRestore(context.Background(), plan)
	if transaction != nil || !errors.Is(err, failure) {
		t.Fatalf("transaction = %v, error = %v", transaction, err)
	}
	assertWorldFile(t, target, "old")
	assertNoRestoreDirectories(t, root)
}

func TestRestoreStoreFailedSwapCanRollback(t *testing.T) {
	root, backupName, target := restoreFixture(t, "world")
	failure := errors.New("install rename failed")
	renames := 0
	store := RestoreStore{rename: func(oldPath, newPath string) error {
		renames++
		if renames == 2 {
			return failure
		}
		return os.Rename(oldPath, newPath)
	}}
	plan, err := store.PrepareRestore(context.Background(), root, "1.20.4", backupName)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.InstallRestore(context.Background(), plan)
	if transaction == nil || !errors.Is(err, failure) {
		t.Fatalf("transaction = %v, error = %v", transaction, err)
	}
	if restored, err := transaction.Rollback(); err != nil || !restored {
		t.Fatal(err)
	}
	assertWorldFile(t, target, "old")
	assertNoRestoreDirectories(t, root)
}

func TestRestoreStoreRejectsInvalidSnapshotBeforeChangingWorld(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, string)
	}{
		{name: "traversal", change: func(_ *testing.T, _, _ string) {}},
		{name: "missing metadata", change: func(t *testing.T, root, backup string) {
			if err := os.Remove(filepath.Join(root, domain.Backups, backup, "backup.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt metadata", change: func(t *testing.T, root, backup string) {
			if err := os.WriteFile(filepath.Join(root, domain.Backups, backup, "backup.json"), []byte("{"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong version", change: func(t *testing.T, root, backup string) {
			writeRestoreMetadata(t, filepath.Join(root, domain.Backups, backup), "1.20.3")
		}},
		{name: "missing world", change: func(t *testing.T, root, backup string) {
			if err := os.RemoveAll(filepath.Join(root, domain.Backups, backup, "world")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, backupName, target := restoreFixture(t, "world")
			test.change(t, root, backupName)
			selected := backupName
			if test.name == "traversal" {
				selected = "../" + backupName
			}
			if _, err := (RestoreStore{}).PrepareRestore(context.Background(), root, "1.20.4", selected); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
			assertWorldFile(t, target, "old")
			assertNoRestoreDirectories(t, root)
		})
	}
}

func TestRestoreStoreRejectsSymlinkInSnapshot(t *testing.T) {
	root, backupName, target := restoreFixture(t, "world")
	link := filepath.Join(root, domain.Backups, backupName, "world", "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := (RestoreStore{}).PrepareRestore(context.Background(), root, "1.20.4", backupName); err == nil {
		t.Fatal("snapshot symlink was accepted")
	}
	assertWorldFile(t, target, "old")
}

func restoreFixture(t *testing.T, worldName string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name="+worldName+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, filepath.FromSlash(worldName))
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "state.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	backupName := "2026-10-02_12-00-00Z"
	backupDirectory := filepath.Join(root, domain.Backups, backupName)
	if err := os.MkdirAll(filepath.Join(backupDirectory, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDirectory, "world", "state.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	writeRestoreMetadata(t, backupDirectory, "1.20.4")
	return root, backupName, target
}

func writeRestoreMetadata(t *testing.T, backupDirectory, version string) {
	t.Helper()
	data, err := json.Marshal(domain.BackupMetadata{
		Game: "minecraft", Version: version, World: "world", CreatedAt: "2026-10-02T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDirectory, "backup.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func assertWorldFile(t *testing.T, world, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(world, "state.txt"))
	if err != nil || string(data) != want {
		t.Fatalf("world state = %q, %v; want %q", data, err, want)
	}
}

func assertNoRestoreDirectories(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-") {
			t.Fatalf("restore directory was not cleaned: %s", entry.Name())
		}
	}
}
