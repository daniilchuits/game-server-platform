package usecases_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"game-server-platform/internal/cli"
	"game-server-platform/internal/domain"
	"game-server-platform/internal/storage"
	"game-server-platform/internal/usecases"
)

func TestStartupDecisions(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		javaError       error
		previousConsent bool
		wantDownload    bool
		wantStart       bool
		wantError       string
	}{
		{name: "unsupported version", input: "1.19.4\n", wantError: "unsupported Minecraft version"},
		{name: "missing Java stops before download", input: "1.20.4\n", javaError: errors.New("download Java 17"), wantError: "Java 17"},
		{name: "declined consent stops launch", input: "1.20.4\nno\n", wantDownload: true},
		{name: "new consent permits launch", input: "1.20.4\nyes\n", wantDownload: true, wantStart: true},
		{name: "saved consent skips prompt", input: "1.20.4\n", previousConsent: true, wantDownload: true, wantStart: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "minecraft", domain.MinecraftVersion)
			files := storage.ServerFiles{}
			if test.previousConsent {
				if err := files.CreateDirectory(directory); err != nil {
					t.Fatal(err)
				}
				if err := files.AcceptEULA(directory); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			runtime := &fakeJava{checkError: test.javaError}
			downloads := &fakeDownloads{}
			app := usecases.StartServer{
				Console: cli.New(bufio.NewReader(strings.NewReader(test.input)), &output),
				Java:    runtime, Downloads: downloads, Files: files, BaseDirectory: root,
			}
			err := app.Run(context.Background())
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("expected %q, got %v", test.wantError, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if downloads.resolved != test.wantDownload {
				t.Errorf("download resolution = %v, want %v", downloads.resolved, test.wantDownload)
			}
			if runtime.started != test.wantStart {
				t.Errorf("server started = %v, want %v", runtime.started, test.wantStart)
			}
			if !test.wantDownload {
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Errorf("created files before prerequisites passed: %v, %v", entries, err)
				}
			}
			if test.wantStart {
				accepted, err := files.EULAAccepted(directory)
				if err != nil || !accepted {
					t.Fatalf("EULA was not saved: %v", err)
				}
				data, err := os.ReadFile(filepath.Join(directory, "server.properties"))
				if err != nil || !strings.Contains(string(data), "online-mode=false") {
					t.Fatalf("offline configuration missing: %s, %v", data, err)
				}
				if runtime.directory != directory {
					t.Errorf("server directory = %q", runtime.directory)
				}
				if !strings.Contains(output.String(), "arbitrary player names") {
					t.Error("offline mode was not explained")
				}
			} else if test.wantDownload {
				accepted, err := files.EULAAccepted(directory)
				if err != nil || accepted {
					t.Fatalf("EULA accepted after decline: %v", err)
				}
			}
			if test.previousConsent && strings.Contains(output.String(), "Do you agree") {
				t.Error("prompted again for saved consent")
			}
		})
	}
}

func TestDownloadErrorStopsStartup(t *testing.T) {
	var output bytes.Buffer
	failure := errors.New("download failed")
	runtime := &fakeJava{}
	app := usecases.StartServer{
		Console: cli.New(bufio.NewReader(strings.NewReader("1.20.4\nyes\n")), &output),
		Java:    runtime, Downloads: &fakeDownloads{downloadError: failure},
		Files: storage.ServerFiles{}, BaseDirectory: t.TempDir(),
	}
	if err := app.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("expected download error, got %v", err)
	}
	if runtime.started || strings.Contains(output.String(), "Do you agree") {
		t.Fatal("startup continued after download error")
	}
}

type fakeJava struct {
	checkError error
	started    bool
	directory  string
}

func (runtime *fakeJava) Check(_ context.Context, minimum int) (domain.JavaInstallation, error) {
	return domain.JavaInstallation{Path: "java", MajorVersion: minimum}, runtime.checkError
}

func (runtime *fakeJava) Start(_ context.Context, _ domain.JavaInstallation, directory string) error {
	runtime.started = true
	runtime.directory = directory
	return nil
}

type fakeDownloads struct {
	resolved      bool
	downloadError error
}

func (downloads *fakeDownloads) Resolve(context.Context, string) (domain.ServerDownload, error) {
	downloads.resolved = true
	return domain.ServerDownload{}, nil
}

func (downloads *fakeDownloads) EnsureJar(context.Context, domain.ServerDownload, string) error {
	return downloads.downloadError
}
