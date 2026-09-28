package java

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

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

func (runtime *Runtime) Launch(ctx context.Context, installation domain.JavaInstallation, directory string) (domain.ServerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command(installation.Path, "-jar", "server.jar", "nogui")
	command.Dir = directory
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open server console: %w", err)
	}
	process := newProcess(stdin)
	stdout := &lineWriter{process: process, destination: runtime.output}
	stderr := &lineWriter{process: process, destination: runtime.errors}
	// Cmd.Wait joins the output-copy goroutines for these writers before returning.
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start server: %w", err)
	}
	go func() {
		err := command.Wait()
		stdout.finish()
		stderr.finish()
		stdin.Close()
		process.complete(err)
	}()
	return process, nil
}

// Start supports the foreground-only startup path. Backup sessions use Launch.
func (runtime *Runtime) Start(ctx context.Context, installation domain.JavaInstallation, directory string) error {
	process, err := runtime.Launch(ctx, installation, directory)
	if err != nil {
		return err
	}
	inputDone := make(chan error, 1)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		for {
			line, err := runtime.input.ReadString('\n')
			if strings.TrimSpace(line) != "" {
				if sendErr := process.Send(strings.TrimRight(line, "\r\n")); sendErr != nil {
					err = sendErr
				}
			}
			if err != nil {
				select {
				case inputDone <- err:
				case <-finished:
				}
				return
			}
			select {
			case <-finished:
				return
			default:
			}
		}
	}()
	select {
	case <-process.Done():
		return process.Wait()
	case <-ctx.Done():
		_ = process.Send("stop")
		return process.Wait()
	case err := <-inputDone:
		_ = process.Send("stop")
		if errors.Is(err, io.EOF) {
			err = nil
		}
		return errors.Join(err, process.Wait())
	}
}
