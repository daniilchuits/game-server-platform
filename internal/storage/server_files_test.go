package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEULAConsentPersistence(t *testing.T) {
	directory := t.TempDir()
	files := ServerFiles{}
	accepted, err := files.EULAAccepted(directory)
	if err != nil || accepted {
		t.Fatalf("missing EULA: accepted=%v, err=%v", accepted, err)
	}
	path := filepath.Join(directory, "eula.txt")
	if err := os.WriteFile(path, []byte("# Keep this comment\neula=true\neula=false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	accepted, err = files.EULAAccepted(directory)
	if err != nil || accepted {
		t.Fatalf("last property must win: accepted=%v, err=%v", accepted, err)
	}
	if err := files.AcceptEULA(directory); err != nil {
		t.Fatal(err)
	}
	accepted, err = files.EULAAccepted(directory)
	if err != nil || !accepted {
		t.Fatalf("consent was not persisted: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# Keep this comment") {
		t.Fatal("EULA comment was removed")
	}
}

func TestOfflineConfigurationPreservesOtherSettings(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.properties")
	contents := "# My server\r\nmax-players=8\r\nmotd=Friends\r\nonline-mode = true\r\nonline-mode:true\r\n"
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	files := ServerFiles{}
	if err := files.ConfigureOffline(directory); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# My server", "max-players=8", "motd=Friends"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("lost setting %q", expected)
		}
	}
	if strings.Count(string(data), "online-mode=false") != 2 {
		t.Fatalf("duplicate settings were not updated: %s", data)
	}
	if err := files.ConfigureOffline(directory); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Fatal("repeated configuration changed the file")
	}
}

func TestConfigurationReadError(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "server.properties"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := (ServerFiles{}).ConfigureOffline(directory); err == nil {
		t.Fatal("expected file read error")
	}
}
