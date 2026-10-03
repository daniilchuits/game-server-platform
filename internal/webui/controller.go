package webui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"game-server-platform/internal/domain"
)

const maxLogEntries = 500
const maxCommandBytes = 4096

var (
	ErrAlreadyRunning     = errors.New("server is already running")
	ErrNotRunning         = errors.New("server is not running")
	ErrServerBusy         = errors.New("server is busy")
	ErrEULANotPending     = errors.New("EULA acceptance is not pending")
	ErrEmptyCommand       = errors.New("command cannot be empty")
	ErrInvalidCommand     = errors.New("command must be one line and no more than 4096 bytes")
	ErrCommandQueueFull   = errors.New("command queue is full")
	ErrRunFuncUnavailable = errors.New("server run function is not configured")
)

const (
	stateStopped  = "stopped"
	stateStarting = "starting"
	stateStopping = "stopping"
	stateFailed   = "failed"
)

// RunFunc wires the transport-independent controller to the existing server
// startup use case.
type RunFunc func(context.Context, *Console) error

// LogEntry is one complete console or process-output line.
type LogEntry struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

// Snapshot is an immutable view suitable for a JSON response. Logs only
// contains entries newer than the cursor supplied to Controller.Snapshot.
type Snapshot struct {
	Version         string     `json:"version"`
	State           string     `json:"state"`
	Running         bool       `json:"running"`
	CanStart        bool       `json:"can_start"`
	CanStop         bool       `json:"can_stop"`
	CanSendCommands bool       `json:"can_send_commands"`
	Error           string     `json:"error"`
	EULAPending     bool       `json:"eula_pending"`
	EULAPath        string     `json:"eula_path"`
	Logs            []LogEntry `json:"logs"`
	Cursor          uint64     `json:"cursor"`
}

type runCycle struct {
	done chan struct{}
	err  error
}

// Controller owns at most one server run and exposes its state to HTTP
// handlers without coupling the use case to the web transport.
type Controller struct {
	parent  context.Context
	version string
	run     RunFunc

	mu          sync.RWMutex
	runID       uint64
	cycle       *runCycle
	cancel      context.CancelFunc
	console     *Console
	state       string
	running     bool
	stopping    bool
	errorText   string
	eulaPending bool
	eulaPath    string
	logs        []LogEntry
	cursor      uint64
}

func NewController(parent context.Context, version string, run RunFunc) *Controller {
	if parent == nil {
		parent = context.Background()
	}
	return &Controller{
		parent:  parent,
		version: version,
		run:     run,
		state:   stateStopped,
		logs:    make([]LogEntry, 0, maxLogEntries),
	}
}

// Start launches one server run in the background.
func (controller *Controller) Start() error {
	controller.mu.Lock()
	if controller.running {
		controller.mu.Unlock()
		return ErrAlreadyRunning
	}
	if controller.run == nil {
		controller.mu.Unlock()
		return ErrRunFuncUnavailable
	}
	if err := controller.parent.Err(); err != nil {
		controller.mu.Unlock()
		return err
	}

	runCtx, cancel := context.WithCancel(controller.parent)
	controller.runID++
	runID := controller.runID
	cycle := &runCycle{done: make(chan struct{})}
	console := newConsole(runCtx, controller.version, consoleCallbacks{
		log: func(source, message string) {
			controller.appendLog(runID, source, message)
		},
		state: func(state domain.SessionState) {
			controller.changeState(runID, state)
		},
		eula: func(pending bool, path string) {
			controller.changeEULA(runID, pending, path)
		},
	})

	controller.cycle = cycle
	controller.cancel = cancel
	controller.console = console
	controller.state = stateStarting
	controller.running = true
	controller.stopping = false
	controller.errorText = ""
	controller.eulaPending = false
	controller.eulaPath = ""
	controller.mu.Unlock()

	go controller.execute(runID, runCtx, cancel, console, cycle)
	return nil
}

// Stop requests graceful cancellation. The run remains active until RunFunc
// returns, so another Start cannot overlap it.
func (controller *Controller) Stop() error {
	controller.mu.Lock()
	if !controller.running {
		controller.mu.Unlock()
		return ErrNotRunning
	}
	cancel := controller.cancel
	controller.stopping = true
	controller.state = stateStopping
	cancel()
	controller.mu.Unlock()
	return nil
}

// Wait waits for the current, or most recently completed, run and returns the
// error produced by RunFunc. It returns ErrNotRunning before the first Start.
func (controller *Controller) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	controller.mu.RLock()
	cycle := controller.cycle
	controller.mu.RUnlock()
	if cycle == nil {
		return ErrNotRunning
	}

	select {
	case <-cycle.done:
		return cycle.err
	default:
	}
	select {
	case <-cycle.done:
		return cycle.err
	case <-ctx.Done():
		select {
		case <-cycle.done:
			return cycle.err
		default:
			return ctx.Err()
		}
	}
}

// SendCommand submits a command to the active server console.
func (controller *Controller) SendCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return ErrEmptyCommand
	}
	if len(command) > maxCommandBytes || strings.ContainsAny(command, "\r\n") {
		return ErrInvalidCommand
	}
	controller.mu.RLock()
	if !controller.running {
		controller.mu.RUnlock()
		return ErrNotRunning
	}
	if controller.stopping || controller.eulaPending || controller.state != string(domain.Running) {
		controller.mu.RUnlock()
		return ErrServerBusy
	}
	console := controller.console
	controller.mu.RUnlock()
	if err := console.enqueueCommand(command); err != nil {
		if errors.Is(err, ErrNotRunning) {
			return ErrNotRunning
		}
		return err
	}
	return nil
}

// AnswerEULA answers the currently visible EULA prompt.
func (controller *Controller) AnswerEULA(accepted bool) error {
	controller.mu.RLock()
	console := controller.console
	pending := controller.running && !controller.stopping && controller.eulaPending
	controller.mu.RUnlock()
	if !pending || console == nil {
		return ErrEULANotPending
	}
	return console.answerEULA(accepted)
}

// Snapshot returns state plus the retained log entries newer than after.
func (controller *Controller) Snapshot(after uint64) Snapshot {
	controller.mu.RLock()
	defer controller.mu.RUnlock()

	logs := make([]LogEntry, 0, len(controller.logs))
	for _, entry := range controller.logs {
		if entry.ID > after {
			logs = append(logs, entry)
		}
	}
	return Snapshot{
		Version:         controller.version,
		State:           controller.state,
		Running:         controller.running,
		CanStart:        !controller.running && controller.run != nil && controller.parent.Err() == nil,
		CanStop:         controller.running && !controller.stopping,
		CanSendCommands: controller.running && !controller.stopping && !controller.eulaPending && controller.state == string(domain.Running),
		Error:           controller.errorText,
		EULAPending:     controller.eulaPending,
		EULAPath:        controller.eulaPath,
		Logs:            logs,
		Cursor:          controller.cursor,
	}
}

func (controller *Controller) execute(
	runID uint64,
	runCtx context.Context,
	cancel context.CancelFunc,
	console *Console,
	cycle *runCycle,
) {
	runErr := controller.run(runCtx, console)
	cancelled := runCtx.Err() != nil
	console.Close()
	cancel()
	controller.finish(runID, cycle, runErr, cancelled)
}

func (controller *Controller) finish(runID uint64, cycle *runCycle, runErr error, cancelled bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	cycle.err = runErr
	if controller.runID == runID {
		controller.running = false
		controller.stopping = false
		controller.cancel = nil
		controller.console = nil
		controller.eulaPending = false
		controller.eulaPath = ""
		if runErr != nil && !(cancelled && isOnlyContextError(runErr)) {
			controller.state = stateFailed
			controller.errorText = runErr.Error()
		} else {
			controller.state = stateStopped
			controller.errorText = ""
		}
	}
	close(cycle.done)
}

func (controller *Controller) appendLog(runID uint64, source, message string) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.runID != runID || !controller.running {
		return
	}
	controller.cursor++
	controller.logs = append(controller.logs, LogEntry{
		ID:      controller.cursor,
		Time:    time.Now().UTC(),
		Source:  source,
		Message: message,
	})
	if overflow := len(controller.logs) - maxLogEntries; overflow > 0 {
		copy(controller.logs, controller.logs[overflow:])
		controller.logs = controller.logs[:maxLogEntries]
	}
}

func (controller *Controller) changeState(runID uint64, state domain.SessionState) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.runID != runID || !controller.running || controller.stopping {
		return
	}
	controller.state = string(state)
}

func (controller *Controller) changeEULA(runID uint64, pending bool, path string) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.runID != runID || !controller.running {
		return
	}
	controller.eulaPending = pending
	if pending {
		controller.eulaPath = path
	} else {
		controller.eulaPath = ""
	}
}

func isOnlyContextError(err error) bool {
	if err == nil || err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !isOnlyContextError(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return isOnlyContextError(wrapped.Unwrap())
	}
	return false
}
