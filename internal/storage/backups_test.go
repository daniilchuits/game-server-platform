package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

func TestBackupStoreCopiesWorldAtomically(t *testing.T) {
	server := t.TempDir()
	world := filepath.Join(server, "custom-world")
	if err := os.MkdirAll(filepath.Join(world, "playerdata"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server, "server.properties"), []byte("level-name=custom-world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(world, "level.dat"), []byte("world data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(world, "playerdata", "one.dat"), []byte("player"), 0644); err != nil {
		t.Fatal(err)
	}

	store := BackupStore{}
	created := time.Date(2026, 9, 28, 12, 30, 0, 0, time.FixedZone("MSK", 3*60*60))
	plan, err := store.Prepare(context.Background(), server, "1.20.4", created)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(plan.Destination, "backups\\2026-09-28_09-30-00Z") && !strings.HasSuffix(plan.Destination, "backups/2026-09-28_09-30-00Z") {
		t.Fatalf("destination = %s", plan.Destination)
	}
	if err := store.Create(context.Background(), plan, "before the cave"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(plan.Destination, "world", "playerdata", "one.dat"))
	if err != nil || string(data) != "player" {
		t.Fatalf("copied player data = %q, %v", data, err)
	}
	message, err := os.ReadFile(filepath.Join(plan.Destination, "backup_message.txt"))
	if err != nil || string(message) != "before the cave" {
		t.Fatalf("message = %q, %v", message, err)
	}
	metadata, err := os.ReadFile(filepath.Join(plan.Destination, "backup.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.BackupMetadata
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Game != "minecraft" || decoded.Version != "1.20.4" || decoded.World != "custom-world" {
		t.Fatalf("metadata = %+v", decoded)
	}
	if _, err := os.Stat(plan.Destination + ".incomplete"); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains: %v", err)
	}
}

func TestBackupStoreUsesUniqueUTCNames(t *testing.T) {
	server := t.TempDir()
	world := filepath.Join(server, "world")
	if err := os.MkdirAll(world, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server, "server.properties"), []byte("level-name=world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := BackupStore{}
	now := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	first, err := store.Prepare(context.Background(), server, "1.20.4", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(first.Destination, 0755); err != nil {
		t.Fatal(err)
	}
	second, err := store.Prepare(context.Background(), server, "1.20.4", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Destination == second.Destination || !strings.HasSuffix(second.Destination, "_1") {
		t.Fatalf("destinations = %s, %s", first.Destination, second.Destination)
	}
}

func TestBackupStoreRejectsUnsafeWorldsAndSymlinks(t *testing.T) {
	server := t.TempDir()
	if err := os.WriteFile(filepath.Join(server, "server.properties"), []byte("level-name=backups\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := (BackupStore{}).Prepare(context.Background(), server, "1.20.4", time.Now()); err == nil {
		t.Fatal("expected backups world to be rejected")
	}

	if err := os.WriteFile(filepath.Join(server, "server.properties"), []byte("level-name=world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(server, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server, "world", "level.dat"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(server, "world", "link")
	if err := os.Symlink(filepath.Join(server, "world", "level.dat"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := (BackupStore{}).Prepare(context.Background(), server, "1.20.4", time.Now()); err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}
