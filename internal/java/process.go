package java

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"game-server-platform/internal/domain"
)

const maxLogLine = 1024 * 1024

// Process owns a single result, broadcast by closing done after output is drained.
type Process struct {
	stdin       io.WriteCloser
	done        chan struct{}
	result      error
	writeMu     sync.Mutex
	outputMu    sync.Mutex
	mu          sync.Mutex
	waiters     map[*outputWaiter]struct{}
	history     []string
	outputError error
}

type outputWaiter struct {
	match  domain.OutputMatcher
	result chan error
}

func newProcess(stdin io.WriteCloser) *Process {
	return &Process{stdin: stdin, done: make(chan struct{}), waiters: make(map[*outputWaiter]struct{})}
}

func (process *Process) complete(err error) {
	process.mu.Lock()
	defer process.mu.Unlock()
	if err != nil {
		err = fmt.Errorf("server process failed: %w", err)
	}
	process.result = errors.Join(err, process.outputError)
	close(process.done)
}

func (process *Process) Done() <-chan struct{} { return process.done }
func (process *Process) Wait() error           { <-process.done; return process.result }

func (process *Process) Send(command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	if strings.ContainsAny(command, "\r\n") {
		return errors.New("server command must contain only one line")
	}
	process.writeMu.Lock()
	defer process.writeMu.Unlock()
	select {
	case <-process.done:
		return errors.New("server has exited")
	default:
	}
	if _, err := io.WriteString(process.stdin, command+"\n"); err != nil {
		return fmt.Errorf("write server command: %w", err)
	}
	return nil
}

// WaitFor replays recent output so an immediately-ready child cannot be missed.
func (process *Process) WaitFor(ctx context.Context, match domain.OutputMatcher, description string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waiter := process.register(match, true)
	return process.await(ctx, waiter, description)
}

// SendAndWait subscribes before writing and never reuses historical replies.
func (process *Process) SendAndWait(ctx context.Context, command string, match domain.OutputMatcher, description string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waiter := process.register(match, false)
	defer process.remove(waiter)
	if err := process.Send(command); err != nil {
		return err
	}
	return process.await(ctx, waiter, description)
}

func (process *Process) register(match domain.OutputMatcher, replay bool) *outputWaiter {
	process.mu.Lock()
	defer process.mu.Unlock()
	waiter := &outputWaiter{match: match, result: make(chan error, 1)}
	if process.outputError != nil {
		waiter.result <- process.outputError
		return waiter
	}
	if replay {
		for _, line := range process.history {
			if matched, err := match(line); matched || err != nil {
				waiter.result <- err
				return waiter
			}
		}
	}
	process.waiters[waiter] = struct{}{}
	return waiter
}

func (process *Process) remove(waiter *outputWaiter) {
	process.mu.Lock()
	defer process.mu.Unlock()
	delete(process.waiters, waiter)
}

func (process *Process) await(ctx context.Context, waiter *outputWaiter, description string) error {
	defer process.remove(waiter)
	select {
	case err := <-waiter.result:
		return err
	case <-process.done:
		// A last reply may arrive immediately before exit; readers are now drained.
		select {
		case err := <-waiter.result:
			return err
		default:
		}
		return fmt.Errorf("server exited before %s: %w", description, errors.Join(errors.New("process ended"), process.result))
	case <-ctx.Done():
		return fmt.Errorf("wait for %s: %w", description, ctx.Err())
	}
}

func (process *Process) emit(line string) {
	process.mu.Lock()
	defer process.mu.Unlock()
	if len(process.history) == 256 {
		process.history = process.history[1:]
	}
	process.history = append(process.history, line)
	for waiter := range process.waiters {
		if matched, err := waiter.match(line); matched || err != nil {
			waiter.result <- err
			delete(process.waiters, waiter)
		}
	}
}

func (process *Process) failOutput(err error) {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.outputError == nil {
		process.outputError = err
	}
	for waiter := range process.waiters {
		waiter.result <- err
		delete(process.waiters, waiter)
	}
}

// lineWriter handles split writes and keeps draining the child even on bad output.
type lineWriter struct {
	process     *Process
	destination io.Writer
	pending     []byte
	discarding  bool
}

func (writer *lineWriter) Write(data []byte) (int, error) {
	length := len(data)
	if writer.destination != nil {
		writer.process.outputMu.Lock()
		n, err := writer.destination.Write(data)
		writer.process.outputMu.Unlock()
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			writer.process.failOutput(fmt.Errorf("forward server output: %w", err))
		}
	}
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		part := data
		if end >= 0 {
			part = data[:end]
		}
		if !writer.discarding {
			if len(writer.pending)+len(part) > maxLogLine {
				writer.pending = nil
				writer.discarding = true
				writer.process.failOutput(errors.New("server log line exceeds 1 MiB"))
			} else {
				writer.pending = append(writer.pending, part...)
			}
		}
		if end < 0 {
			break
		}
		if !writer.discarding {
			writer.process.emit(strings.TrimSuffix(string(writer.pending), "\r"))
		}
		writer.pending = nil
		writer.discarding = false
		data = data[end+1:]
	}
	return length, nil
}

func (writer *lineWriter) finish() {
	if len(writer.pending) > 0 && !writer.discarding {
		writer.process.emit(strings.TrimSuffix(string(writer.pending), "\r"))
	}
	writer.pending = nil
}
