// Package webui adapts the server's interactive console to a browser-friendly
// controller.
package webui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	"game-server-platform/internal/domain"
)

const (
	commandBufferSize       = 64
	maxBufferedLogLineBytes = 32 << 10
)

var errEULAAlreadyPending = errors.New("an EULA answer is already pending")

type consoleCallbacks struct {
	log   func(source, message string)
	state func(domain.SessionState)
	eula  func(pending bool, path string)
}

type eulaRequest struct {
	answer chan bool
}

// Console implements the interactive console required by the server use case.
// Browser actions provide commands and EULA answers through its Controller.
type Console struct {
	ctx      context.Context
	version  string
	commands chan string
	done     chan struct{}
	callback consoleCallbacks

	mu      sync.Mutex
	closed  bool
	eula    *eulaRequest
	writers map[*logLineWriter]struct{}
}

func newConsole(ctx context.Context, version string, callback consoleCallbacks) *Console {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Console{
		ctx:      ctx,
		version:  version,
		commands: make(chan string, commandBufferSize),
		done:     make(chan struct{}),
		callback: callback,
		writers:  make(map[*logLineWriter]struct{}),
	}
}

// ReadVersion supplies the version selected when the controller was created.
func (console *Console) ReadVersion() (string, error) {
	select {
	case <-console.ctx.Done():
		return "", console.ctx.Err()
	default:
	}
	select {
	case <-console.done:
		return "", io.EOF
	default:
		return console.version, nil
	}
}

// ConfirmEULA publishes a pending browser prompt and waits until it is
// answered, the run is cancelled, or the console is closed.
func (console *Console) ConfirmEULA(path string) (bool, error) {
	request := &eulaRequest{answer: make(chan bool, 1)}

	console.mu.Lock()
	if console.closed {
		console.mu.Unlock()
		return false, io.EOF
	}
	if err := console.ctx.Err(); err != nil {
		console.mu.Unlock()
		return false, err
	}
	if console.eula != nil {
		console.mu.Unlock()
		return false, errEULAAlreadyPending
	}
	console.eula = request
	console.publishEULALocked(true, path)
	console.mu.Unlock()

	defer console.clearEULA(request)
	select {
	case accepted := <-request.answer:
		return accepted, nil
	case <-console.ctx.Done():
		return false, console.ctx.Err()
	case <-console.done:
		return false, io.EOF
	}
}

// Message records a use-case message in the controller's system log.
func (console *Console) Message(message string) {
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.closed {
		return
	}
	console.publishTextLocked("system", message)
}

// ReadCommand blocks until a browser client submits a command or the active
// run ends.
func (console *Console) ReadCommand() (string, error) {
	select {
	case <-console.ctx.Done():
		return "", console.ctx.Err()
	case <-console.done:
		return "", io.EOF
	default:
	}

	select {
	case command := <-console.commands:
		return command, nil
	case <-console.ctx.Done():
		return "", console.ctx.Err()
	case <-console.done:
		return "", io.EOF
	}
}

// SessionStateChanged lets the server workflow publish a structured state
// instead of requiring the web layer to parse human-readable messages.
func (console *Console) SessionStateChanged(state domain.SessionState) {
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.closed || console.callback.state == nil {
		return
	}
	console.callback.state(state)
}

// Writer returns a line-buffered destination for process output. Each writer
// keeps its own partial line, so stdout and stderr chunks cannot corrupt one
// another.
func (console *Console) Writer(source string) io.Writer {
	writer := &logLineWriter{
		source: strings.TrimSpace(source),
		emit:   console.publishLogLine,
	}
	if writer.source == "" {
		writer.source = "server"
	}

	console.mu.Lock()
	if console.closed {
		writer.closed = true
	} else {
		console.writers[writer] = struct{}{}
	}
	console.mu.Unlock()
	return writer
}

// Close releases all pending console reads and flushes final unterminated log
// lines. It is safe to call more than once.
func (console *Console) Close() {
	console.mu.Lock()
	if console.closed {
		console.mu.Unlock()
		return
	}
	console.closed = true
	close(console.done)
	if console.eula != nil {
		console.eula = nil
		console.publishEULALocked(false, "")
	}
	writers := make([]*logLineWriter, 0, len(console.writers))
	for writer := range console.writers {
		writers = append(writers, writer)
	}
	console.mu.Unlock()

	for _, writer := range writers {
		writer.close()
	}
}

func (console *Console) enqueueCommand(command string) error {
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.closed || console.ctx.Err() != nil {
		return ErrNotRunning
	}
	select {
	case console.commands <- command:
		return nil
	default:
		return ErrCommandQueueFull
	}
}

func (console *Console) answerEULA(accepted bool) error {
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.closed || console.ctx.Err() != nil || console.eula == nil {
		return ErrEULANotPending
	}
	request := console.eula
	console.eula = nil
	request.answer <- accepted
	console.publishEULALocked(false, "")
	return nil
}

func (console *Console) clearEULA(request *eulaRequest) {
	console.mu.Lock()
	defer console.mu.Unlock()
	if console.eula != request {
		return
	}
	console.eula = nil
	console.publishEULALocked(false, "")
}

func (console *Console) publishEULALocked(pending bool, path string) {
	if console.callback.eula != nil {
		console.callback.eula(pending, path)
	}
}

func (console *Console) publishTextLocked(source, message string) {
	if console.callback.log == nil {
		return
	}
	message = strings.ToValidUTF8(message, "\uFFFD")
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	lines := strings.Split(message, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		console.callback.log(source, truncateLogLine(line, false))
	}
}

func (console *Console) publishLogLine(source, message string) {
	if console.callback.log != nil {
		console.callback.log(source, message)
	}
}

type logLineWriter struct {
	mu        sync.Mutex
	source    string
	emit      func(source, message string)
	buffer    []byte
	truncated bool
	closed    bool
}

func (writer *logLineWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, io.ErrClosedPipe
	}

	start := 0
	for index, value := range data {
		if value != '\n' {
			continue
		}
		writer.append(data[start:index])
		writer.flush()
		start = index + 1
	}
	writer.append(data[start:])
	return len(data), nil
}

func (writer *logLineWriter) append(data []byte) {
	remaining := maxBufferedLogLineBytes - len(writer.buffer)
	if remaining <= 0 {
		writer.truncated = writer.truncated || len(data) > 0
		return
	}
	if len(data) > remaining {
		writer.buffer = append(writer.buffer, data[:remaining]...)
		writer.truncated = true
		return
	}
	writer.buffer = append(writer.buffer, data...)
}

func (writer *logLineWriter) flush() {
	line := writer.buffer
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	message := strings.ToValidUTF8(string(line), "\uFFFD")
	if writer.truncated {
		message = truncateLogLine(message, true)
	}
	if writer.emit != nil {
		writer.emit(writer.source, message)
	}
	writer.buffer = writer.buffer[:0]
	writer.truncated = false
}

func (writer *logLineWriter) close() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return
	}
	writer.closed = true
	if len(writer.buffer) > 0 || writer.truncated {
		writer.flush()
	}
}

func truncateLogLine(line string, alreadyTruncated bool) string {
	if len(line) > maxBufferedLogLineBytes {
		line = strings.ToValidUTF8(line[:maxBufferedLogLineBytes], "\uFFFD")
		alreadyTruncated = true
	}
	if alreadyTruncated {
		return line + " [truncated]"
	}
	return line
}
