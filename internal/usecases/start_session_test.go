package usecases

import (
	"context"
	"errors"
	"io"
	"testing"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/storage"
)

type setupJava struct{}

func (setupJava) Check(context.Context, int) (domain.JavaInstallation, error) {
	return domain.JavaInstallation{Path: "java", MajorVersion: 17}, nil
}
func (setupJava) Start(context.Context, domain.JavaInstallation, string) error {
	return errors.New("startup bypassed the session")
}

type setupDownloads struct{}

func (setupDownloads) Resolve(context.Context, string) (domain.ServerDownload, error) {
	return domain.ServerDownload{}, nil
}
func (setupDownloads) EnsureJar(context.Context, domain.ServerDownload, string) error { return nil }

func TestStartupUsesProcessSessionAndRetainsOwnershipOnCancellation(t *testing.T) {
	for _, mode := range []string{"ready then EOF", "EOF before ready", "launch failure"} {
		t.Run(mode, func(t *testing.T) {
			process := newControlledProcess()
			if mode == "EOF before ready" {
				process.ready = make(chan struct{})
			}
			launcher := &controlledLauncher{next: process, launched: make(chan struct{}, 1)}
			failure := errors.New("Java launch failed")
			if mode == "launch failure" {
				launcher.err = failure
			}
			console := newSessionConsole()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() { close(console.input); process.finish(nil) })
			app := StartServer{
				Console: console, Java: setupJava{}, Downloads: setupDownloads{},
				ServerFiles: storage.ServerFiles{}, BaseDirectory: t.TempDir(),
				Process: launcher, Backups: controlledStore{}, BackupsReader: controlledBackupsReader{},
				Restorer: controlledRestorer{}, Clock: controlledClock{},
			}
			done := make(chan error, 1)
			go func() { done <- app.Run(ctx) }()
			receive(t, launcher.launched)
			if mode == "launch failure" {
				if err := receive(t, done); !errors.Is(err, failure) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if mode == "ready then EOF" {
				waitMessage(t, console, "Server ready.")
			}
			console.input <- commandRead{err: io.EOF}
			if err := receive(t, done); err != nil {
				t.Fatal(err)
			}
			waitCommand(t, process, "stop")
		})
	}
}
