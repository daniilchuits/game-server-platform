package java

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"game-server-platform/internal/domain"
)

type Runtime struct {
	checker
	input  *bufio.Reader
	output io.Writer
	errors io.Writer
}

func New(input *bufio.Reader, output, errors io.Writer) *Runtime {
	return &Runtime{checker: newChecker(), input: input, output: output, errors: errors}
}

func (runtime *Runtime) Start(ctx context.Context, installation domain.JavaInstallation, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.Command(installation.Path, "-jar", "server.jar", "nogui")
	command.Dir = directory
	command.Stdout = runtime.output
	command.Stderr = runtime.errors
	return runtime.run(ctx, command)
}

func (runtime *Runtime) run(ctx context.Context, command *exec.Cmd) error {
	input, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open server console: %w", err)
	}
	defer input.Close()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	// Share the prompt reader so commands buffered during setup are not lost.
	// Do not wait for terminal input when the server exits by itself.
	var writeLock sync.Mutex
	go func() {
		for {
			line, readErr := runtime.input.ReadString('\n')
			writeLock.Lock()
			_, writeErr := io.WriteString(input, line)
			writeLock.Unlock()
			if readErr != nil || writeErr != nil {
				return
			}
		}
	}()
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()

	select {
	case err = <-finished:
	case <-ctx.Done():
		fmt.Fprintln(runtime.output, "Stopping server; waiting for the world to save...")
		writeLock.Lock()
		_, _ = io.WriteString(input, "stop\n")
		writeLock.Unlock()
		err = <-finished
	}
	if err != nil {
		return fmt.Errorf("server process failed: %w", err)
	}
	return nil
}
