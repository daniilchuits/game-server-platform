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
	app := usecases.StartServer{
		Console:       cli.New(input, os.Stdout),
		Java:          java.New(input, os.Stdout, os.Stderr),
		Downloads:     mojang.New(),
		Files:         storage.ServerFiles{},
		BaseDirectory: "game-server-platform",
	}
	if err := app.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		stop()
		os.Exit(1)
	}
}
