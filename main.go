package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"

	"game-server-platform/internal/cli"
	"game-server-platform/internal/java"
	"game-server-platform/internal/mojang"
	"game-server-platform/internal/storage"
	"game-server-platform/internal/usecases"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	input := bufio.NewReader(os.Stdin)
	runtime := java.New(input, os.Stdout, os.Stderr)

	app := usecases.StartServer{
		Console:       cli.New(input, os.Stdout),
		Java:          runtime,
		Downloads:     mojang.New(),
		ServerFiles:   storage.ServerFiles{},
		Process:       runtime,
		Backups:       storage.BackupStore{},
		BackupsReader: storage.BackupsReader{},
		Restorer:      storage.RestoreStore{},
		Clock:         usecases.RealClock{},
		BaseDirectory: "game-server-platform",
	}
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		stop()
		os.Exit(1)
	}
}
