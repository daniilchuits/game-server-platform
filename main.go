package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/java"
	"game-server-platform/internal/mojang"
	"game-server-platform/internal/storage"
	"game-server-platform/internal/usecases"
	"game-server-platform/internal/webui"
)

const (
	baseDirectory = "game-server-platform"
	webAddress    = "127.0.0.1:8080"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Restore the default signal behavior after the first Ctrl+C. Graceful
	// shutdown still owns the process, while a second Ctrl+C remains an escape
	// hatch if Java cannot exit.
	go func() {
		<-ctx.Done()
		stop()
	}()

	backupsReader := storage.BackupsReader{}
	controller := webui.NewController(ctx, domain.MinecraftVersion, func(runCtx context.Context, console *webui.Console) error {
		runtime := java.New(
			bufio.NewReader(os.Stdin),
			console.Writer("minecraft"),
			console.Writer("minecraft-error"),
		)
		app := usecases.StartServer{
			Console:       console,
			Java:          runtime,
			Downloads:     mojang.New(),
			ServerFiles:   storage.ServerFiles{},
			Process:       runtime,
			Backups:       storage.BackupStore{},
			BackupsReader: backupsReader,
			Restorer:      storage.RestoreStore{},
			Clock:         usecases.RealClock{},
			Observer:      console,
			BaseDirectory: baseDirectory,
		}
		return app.Run(runCtx)
	})

	serverDirectory := filepath.Join(baseDirectory, "minecraft", domain.MinecraftVersion)
	server := webui.NewServer(webAddress, controller, func() ([]domain.BackupData, error) {
		return backupsReader.ReadBackups(serverDirectory)
	})
	fmt.Println("Game Server Platform control panel:", server.URL())
	if err := server.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		stop()
		os.Exit(1)
	}
}
