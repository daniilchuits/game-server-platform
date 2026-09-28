package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

func prepareWorld(t *testing.T) domain.BackupPlan {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	plan, err := (BackupStore{}).Prepare(context.Background(), root, "1.20.4", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBackupFailuresDiscardOnlyTheirTemporaryCopy(t *testing.T) {
	for _, failure := range []string{"copy", "metadata", "message", "cancel", "collision"} {
		t.Run(failure, func(t *testing.T) {
			plan := prepareWorld(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := BackupStore{copyWorld: func(ctx context.Context, source, target string) error {
				if err := copyTree(ctx, source, target); err != nil {
					return err
				}
				switch failure {
				case "copy":
					return errors.New("copy failed")
				case "metadata":
					return os.Mkdir(filepath.Join(filepath.Dir(target), "backup.json"), 0755)
				case "message":
					return os.Mkdir(filepath.Join(filepath.Dir(target), "backup_message.txt"), 0755)
				case "cancel":
					cancel()
				case "collision":
					return os.Mkdir(plan.Destination, 0755)
				}
				return nil
			}}
			if err := store.Create(ctx, plan, "note"); err == nil {
				t.Fatal("failure was ignored")
			}
			entries, err := os.ReadDir(filepath.Dir(plan.Destination))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".incomplete-") {
					t.Fatalf("incomplete copy remained: %s", entry.Name())
				}
			}
			if failure == "collision" {
				if _, err := os.Stat(plan.Destination); err != nil {
					t.Fatalf("removed pre-existing destination: %v", err)
				}
			} else if _, err := os.Stat(plan.Destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed snapshot published: %v", err)
			}
		})
	}
}

func TestBackupDoesNotOverwriteExistingSnapshot(t *testing.T) {
	plan := prepareWorld(t)
	if err := (BackupStore{}).Create(context.Background(), plan, "original"); err != nil {
		t.Fatal(err)
	}
	if err := (BackupStore{}).Create(context.Background(), plan, "overwrite"); err == nil {
		t.Fatal("overwrote completed snapshot")
	}
	data, err := os.ReadFile(filepath.Join(plan.Destination, "backup_message.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("message = %q, %v", data, err)
	}
}

func TestBackupRejectsForgedAndChangedPaths(t *testing.T) {
	for _, failure := range []string{"outside world", "world is root", "outside destination", "world disappeared", "backups is file"} {
		t.Run(failure, func(t *testing.T) {
			plan := prepareWorld(t)
			switch failure {
			case "outside world":
				plan.WorldDirectory = t.TempDir()
			case "world is root":
				plan.WorldDirectory = plan.ServerDirectory
			case "outside destination":
				plan.Destination = filepath.Join(t.TempDir(), "snapshot")
			case "world disappeared":
				if err := os.Remove(plan.WorldDirectory); err != nil {
					t.Fatal(err)
				}
			case "backups is file":
				backups := filepath.Dir(plan.Destination)
				if err := os.Remove(backups); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(backups, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := (BackupStore{}).Create(context.Background(), plan, ""); err == nil {
				t.Fatal("unsafe backup accepted")
			}
		})
	}
}

func TestBackupReadsEscapedNestedWorldAndCopiesAllDimensions(t *testing.T) {
	root := t.TempDir()
	name := "maps/Мир 🌍"
	world := filepath.Join(root, filepath.FromSlash(name))
	for _, directory := range []string{"region", "DIM-1/region", "DIM1/region", "dimensions/custom/moon/data", "playerdata", "empty"} {
		if err := os.MkdirAll(filepath.Join(world, filepath.FromSlash(directory)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"level.dat": "level", "level.dat_old": "previous level",
		"region/r.0.0.mca": "overworld", "DIM-1/region/r.0.0.mca": "nether",
		"DIM1/region/r.0.0.mca": "end", "dimensions/custom/moon/data/state.dat": "custom",
		"playerdata/player.dat": "player\x00binary", "session.lock": "lock",
	}
	for path, contents := range files {
		if err := os.WriteFile(filepath.Join(world, filepath.FromSlash(path)), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name=maps/\\u041c\\u0438\\u0440 \\uD83C\\uDF0D\n"), 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := (BackupStore{}).Prepare(context.Background(), root, "1.20.4", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := (BackupStore{}).Create(context.Background(), plan, ""); err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		for _, base := range []string{world, filepath.Join(plan.Destination, "world")} {
			got, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(path)))
			if err != nil || string(got) != want {
				t.Errorf("%s: got %q, %v", path, got, err)
			}
		}
	}
	if info, err := os.Stat(filepath.Join(plan.Destination, "world", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plan.Destination, "backup_message.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected message file: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(plan.Destination, "backup.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata domain.BackupMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.World != name || metadata.CreatedAt != plan.CreatedAt {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestWorldDirectoryRejectsUnsafeAndMalformedNames(t *testing.T) {
	for _, name := range []string{".", "..", "../outside", "backups", "BACKUPS/old", "/outside", "\\u12XY", "unfinished\\"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name="+name+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := (ServerFiles{}).WorldDirectory(root); err == nil {
				t.Fatal("unsafe or invalid name accepted")
			}
		})
	}
}

func TestContextReaderStopsDuringLargeFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := contextReader{ctx: ctx, reader: strings.NewReader(strings.Repeat("world", 100000))}
	data := make([]byte, 4096)
	if _, err := reader.Read(data); err != nil {
		t.Fatal(err)
	}
	cancel()
	if n, err := reader.Read(data); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel = %d, %v", n, err)
	}
}

func TestCopyFileCancellationAndExistingDestination(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyFile(ctx, source, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("copy = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created cancelled file: %v", err)
	}
	if err := copyFile(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(context.Background(), source, target); err == nil {
		t.Fatal("overwrote existing destination file")
	}
}

func TestBackupPrepareReportsMissingWorldAndInvalidBackupDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := (BackupStore{}).Prepare(context.Background(), root, "1.20.4", time.Now()); err == nil {
		t.Fatal("missing world accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "world"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backups"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (BackupStore{}).Prepare(context.Background(), root, "1.20.4", time.Now()); err == nil {
		t.Fatal("backup path is a file")
	}
}

func TestDecodePropertyEscapes(t *testing.T) {
	value, err := decodePropertyValue("hello\\ world\\\\sub\\t\\n\\r\\f")
	if err != nil || value != "hello world\\sub\t\n\r\f" {
		t.Fatalf("decoded = %q, %v", value, err)
	}
	if _, err := decodePropertyValue("\\u12"); err == nil {
		t.Fatal("accepted incomplete Unicode")
	}
}

// Ensure the cancellation wrapper is used as a reader, without WriterTo's
// optimized path bypassing Read and its context check.
var _ io.Reader = contextReader{}
