package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupRejectsWindowsJunctions(t *testing.T) {
	for _, location := range []string{"world", "ancestor", "inside world", "backups"} {
		t.Run(location, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("external"), 0644); err != nil {
				t.Fatal(err)
			}
			world := "world"
			junction := filepath.Join(root, "world")
			switch location {
			case "ancestor":
				world = "maps/world"
				if err := os.Mkdir(filepath.Join(outside, "world"), 0755); err != nil {
					t.Fatal(err)
				}
				junction = filepath.Join(root, "maps")
			case "inside world", "backups":
				if err := os.Mkdir(filepath.Join(root, "world"), 0755); err != nil {
					t.Fatal(err)
				}
				junction = filepath.Join(root, "world", "link")
				if location == "backups" {
					junction = filepath.Join(root, "backups")
				}
			}
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
				"New-Item -ItemType Junction -Path "+quote(junction)+" -Target "+quote(outside)+" -ErrorAction Stop | Out-Null")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create test junction: %v: %s", err, output)
			}
			// Remove only the junction itself before TempDir cleanup.
			t.Cleanup(func() {
				if err := os.Remove(junction); err != nil {
					t.Errorf("remove junction: %v", err)
				}
			})
			if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name="+world+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := (BackupStore{}).Prepare(context.Background(), root, "1.20.4", time.Now()); err == nil || !strings.Contains(err.Error(), "junction") {
				t.Fatalf("accepted junction: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(outside, "keep.txt"))
			if err != nil || string(data) != "external" {
				t.Fatalf("external data changed: %q, %v", data, err)
			}
		})
	}
}

func TestRestoreRejectsWindowsJunctionInSnapshot(t *testing.T) {
	root, backupName, target := restoreFixture(t, "world")
	source := filepath.Join(root, "backups", backupName, "world")
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "state.txt"), []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path "+quote(source)+" -Target "+quote(outside)+" -ErrorAction Stop | Out-Null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create test junction: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(source); err != nil {
			t.Errorf("remove junction: %v", err)
		}
	})
	if _, err := (RestoreStore{}).PrepareRestore(context.Background(), root, "1.20.4", backupName); err == nil || !strings.Contains(err.Error(), "junction") {
		t.Fatalf("accepted snapshot junction: %v", err)
	}
	assertWorldFile(t, target, "old")
}
