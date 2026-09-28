package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupKeepsPreviousIncompleteDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	store := BackupStore{}
	plan, err := store.Prepare(context.Background(), root, "1.20.4", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	previous := plan.Destination + ".incomplete"
	if err := os.Mkdir(previous, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(previous, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("previous attempt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(context.Background(), plan, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("deleted someone else's incomplete backup: %v", err)
	}
}

func TestCancelledBackupDoesNotPublishEmptyWorld(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	store := BackupStore{}
	plan, err := store.Prepare(context.Background(), root, "1.20.4", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Create(ctx, plan, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(plan.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled backup was published: %v", err)
	}
}
