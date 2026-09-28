package usecases

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

const readyLine = "[12:00:00] [Server thread/INFO]: Done (1.234s)! For help, type \"help\""

type controlledProcess struct {
	mu                sync.Mutex
	commands          []string
	sent              chan string
	done              chan struct{}
	once              sync.Once
	exitError         error
	stopAutomatically bool
	ready             <-chan struct{}
	ack               func(context.Context, string) (string, error)
}

func newControlledProcess() *controlledProcess {
	return &controlledProcess{done: make(chan struct{}), sent: make(chan string, 64), stopAutomatically: true}
}
func (process *controlledProcess) finish(err error) {
	process.once.Do(func() { process.exitError = err; close(process.done) })
}
func (process *controlledProcess) Send(command string) error {
	process.mu.Lock()
	process.commands = append(process.commands, command)
	process.mu.Unlock()
	process.sent <- command
	if command == "stop" && process.stopAutomatically {
		process.finish(nil)
	}
	return nil
}
func (process *controlledProcess) SendAndWait(ctx context.Context, command string, match domain.OutputMatcher, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := process.Send(command); err != nil {
		return err
	}
	response := map[string]string{"save-off": "Automatic saving is now disabled", "save-all flush": "Saved the game", "save-on": "Automatic saving is now enabled"}[command]
	if process.ack != nil {
		var err error
		response, err = process.ack(ctx, command)
		if err != nil {
			return err
		}
	}
	matched, err := match("[12:00:00] [Server thread/INFO]: " + response)
	if err != nil {
		return err
	}
	if !matched {
		return errors.New("fake response did not match")
	}
	return nil
}
func (process *controlledProcess) WaitFor(ctx context.Context, match domain.OutputMatcher, _ string) error {
	if process.ready != nil {
		select {
		case <-process.ready:
		case <-ctx.Done():
			return ctx.Err()
		case <-process.done:
			return errors.New("exited before ready")
		}
	}
	matched, err := match(readyLine)
	if err != nil {
		return err
	}
	if !matched {
		return errors.New("fake readiness did not match")
	}
	return ctx.Err()
}
func (process *controlledProcess) Done() <-chan struct{} { return process.done }
func (process *controlledProcess) Wait() error           { <-process.done; return process.exitError }
func (process *controlledProcess) recorded() []string {
	process.mu.Lock()
	defer process.mu.Unlock()
	return append([]string(nil), process.commands...)
}

type controlledLauncher struct {
	next     *controlledProcess
	err      error
	launched chan struct{}
}

func (launcher *controlledLauncher) Launch(context.Context, domain.JavaInstallation, string) (ServerProcess, error) {
	if launcher.launched != nil {
		launcher.launched <- struct{}{}
	}
	if launcher.err != nil {
		return nil, launcher.err
	}
	return launcher.next, nil
}

type controlledStore struct {
	prepareError error
	create       func(context.Context, domain.BackupPlan, string) error
}

func (store controlledStore) Prepare(ctx context.Context, _, version string, _ time.Time) (domain.BackupPlan, error) {
	if store.prepareError != nil {
		return domain.BackupPlan{}, store.prepareError
	}
	return domain.BackupPlan{Destination: "snapshot", Version: version}, ctx.Err()
}
func (store controlledStore) Create(ctx context.Context, plan domain.BackupPlan, message string) error {
	if store.create != nil {
		return store.create(ctx, plan, message)
	}
	return ctx.Err()
}

type controlledClock struct {
	sleep func(context.Context, time.Duration) error
}

func (controlledClock) Now() time.Time { return time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC) }
func (clock controlledClock) Sleep(ctx context.Context, duration time.Duration) error {
	if clock.sleep != nil {
		return clock.sleep(ctx, duration)
	}
	return ctx.Err()
}

func backupSession() (Session, *controlledProcess, *controlledProcess) {
	initial, restarted := newControlledProcess(), newControlledProcess()
	session := Session{
		Process: &controlledLauncher{next: restarted}, Backups: controlledStore{},
		Clock: controlledClock{}, Version: "1.20.4", WaitTimeout: time.Second,
	}
	return session, initial, restarted
}

func receive[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for test event")
		var zero T
		return zero
	}
}
func waitCommand(t *testing.T, process *controlledProcess, command string) {
	t.Helper()
	for receive(t, process.sent) != command {
	}
}
func assertNotCompleted[T any](t *testing.T, values <-chan T) {
	t.Helper()
	select {
	case value := <-values:
		t.Fatalf("operation completed too early: %v", value)
	default:
	}
}

func TestBackupWaitsForSaveAndSuccessfulExitBeforeCopy(t *testing.T) {
	session, initial, restarted := backupSession()
	save := make(chan struct{})
	initial.stopAutomatically = false
	initial.ack = func(ctx context.Context, command string) (string, error) {
		if command == "save-all flush" {
			select {
			case <-save:
				return "Saved the game", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return "Automatic saving is now disabled", nil
	}
	copied := make(chan string, 1)
	session.Backups = controlledStore{create: func(_ context.Context, _ domain.BackupPlan, message string) error {
		select {
		case <-initial.Done():
		default:
			return errors.New("copy attempted while Java was running")
		}
		copied <- message
		return nil
	}}
	done := make(chan BackupResult, 1)
	go func() { done <- CreateBackup(context.Background(), session, initial, "before  пещера 🌍") }()
	waitCommand(t, initial, "save-all flush")
	assertNotCompleted(t, copied)
	for _, command := range initial.recorded() {
		if command == "stop" {
			t.Fatal("stop preceded save acknowledgement")
		}
	}
	close(save)
	waitCommand(t, initial, "stop")
	assertNotCompleted(t, copied)
	initial.finish(nil)
	result := receive(t, done)
	if result.Err != nil || result.Path != "snapshot" || result.Process != restarted {
		t.Fatalf("result = %+v", result)
	}
	if message := receive(t, copied); message != "before  пещера 🌍" {
		t.Fatalf("message = %q", message)
	}
	want := []string{
		"say Backup and restart in 10 seconds. Please reconnect afterward.",
		"say Backup and restart in 5 seconds. Please reconnect afterward.",
		"save-off", "save-all flush", "stop",
	}
	if got := initial.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q", got)
	}
}

func TestSaveFailureRecoversOnlyWhenSavingWasChanged(t *testing.T) {
	for _, alreadyOff := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "already off"}[alreadyOff], func(t *testing.T) {
			session, initial, _ := backupSession()
			initial.ack = func(_ context.Context, command string) (string, error) {
				switch command {
				case "save-off":
					if alreadyOff {
						return "Saving is already turned off", nil
					}
					return "Automatic saving is now disabled", nil
				case "save-all flush":
					return "Unable to save the game (is there enough disk space?)", nil
				case "save-on":
					return "Automatic saving is now enabled", nil
				}
				return "", errors.New("unexpected command")
			}
			result := CreateBackup(context.Background(), session, initial, "")
			if result.Err == nil || result.Fatal || result.Process != initial || result.Path != "" {
				t.Fatalf("result = %+v", result)
			}
			commands := strings.Join(initial.recorded(), "|")
			if strings.Contains(commands, "stop") || strings.Contains(commands, "save-on") == alreadyOff {
				t.Fatalf("commands = %s", commands)
			}
		})
	}
}

func TestUnconfirmedSavingRecoveryRequiresShutdown(t *testing.T) {
	session, initial, _ := backupSession()
	initial.ack = func(_ context.Context, command string) (string, error) {
		if command == "save-off" {
			return "Automatic saving is now disabled", nil
		}
		return "", context.DeadlineExceeded
	}
	result := CreateBackup(context.Background(), session, initial, "")
	if !result.Fatal || !strings.Contains(result.Err.Error(), "recovery") {
		t.Fatalf("result = %+v", result)
	}
	if got := initial.recorded(); got[len(got)-1] != "save-on" {
		t.Fatalf("commands = %q", got)
	}
}

func TestBackupRestartsAfterCopyFailure(t *testing.T) {
	session, initial, restarted := backupSession()
	failure := errors.New("disk full")
	session.Backups = controlledStore{create: func(context.Context, domain.BackupPlan, string) error { return failure }}
	result := CreateBackup(context.Background(), session, initial, "")
	if !errors.Is(result.Err, failure) || result.Fatal || result.Path != "" || result.Process != restarted {
		t.Fatalf("result = %+v", result)
	}
}

func TestRestartFailurePreservesSnapshotAndProcessOwnership(t *testing.T) {
	for _, startupTimeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch error", true: "readiness timeout"}[startupTimeout], func(t *testing.T) {
			session, initial, restarted := backupSession()
			launcher := session.Process.(*controlledLauncher)
			if startupTimeout {
				restarted.ready = make(chan struct{})
				session.WaitTimeout = 20 * time.Millisecond
			} else {
				launcher.err = errors.New("cannot launch Java")
			}
			result := CreateBackup(context.Background(), session, initial, "")
			if result.Path != "snapshot" || result.Err == nil || !result.Fatal {
				t.Fatalf("result = %+v", result)
			}
			if startupTimeout && result.Process != restarted {
				t.Fatal("lost ownership of unready Java process")
			}
		})
	}
}

func TestShutdownTimeoutWaitsForLateExitAndRestartsWithoutCopy(t *testing.T) {
	session, initial, restarted := backupSession()
	initial.stopAutomatically = false
	session.WaitTimeout = 20 * time.Millisecond
	stalled := make(chan struct{}, 1)
	session.progress = func(state domain.SessionState) {
		if strings.Contains(string(state), "deadline exceeded") {
			stalled <- struct{}{}
		}
	}
	session.Backups = controlledStore{create: func(context.Context, domain.BackupPlan, string) error {
		return errors.New("must not copy timed-out backup")
	}}
	done := make(chan BackupResult, 1)
	go func() { done <- CreateBackup(context.Background(), session, initial, "") }()
	receive(t, stalled)
	assertNotCompleted(t, done)
	initial.finish(nil)
	result := receive(t, done)
	if result.Err == nil || result.Fatal || result.Path != "" || result.Process != restarted {
		t.Fatalf("result = %+v", result)
	}
}

func TestFailedServerExitNeverCopiesOrRestarts(t *testing.T) {
	session, initial, _ := backupSession()
	initial.stopAutomatically = false
	copied := make(chan struct{}, 1)
	session.Backups = controlledStore{create: func(context.Context, domain.BackupPlan, string) error { copied <- struct{}{}; return nil }}
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	done := make(chan BackupResult, 1)
	go func() { done <- CreateBackup(context.Background(), session, initial, "") }()
	waitCommand(t, initial, "stop")
	failure := errors.New("exit status 1")
	initial.finish(failure)
	result := receive(t, done)
	if !errors.Is(result.Err, failure) || !result.Fatal {
		t.Fatalf("result = %+v", result)
	}
	assertNotCompleted(t, copied)
	assertNotCompleted(t, launched)
}

type sessionConsole struct {
	input    chan commandRead
	messages chan string
}

func newSessionConsole() *sessionConsole {
	return &sessionConsole{input: make(chan commandRead, 16), messages: make(chan string, 64)}
}
func (*sessionConsole) ReadVersion() (string, error)     { return "1.20.4", nil }
func (*sessionConsole) ConfirmEULA(string) (bool, error) { return true, nil }
func (console *sessionConsole) ReadCommand() (string, error) {
	read, ok := <-console.input
	if !ok {
		return "", io.EOF
	}
	return read.line, read.err
}
func (console *sessionConsole) Message(message string) { console.messages <- message }
func waitMessage(t *testing.T, console *sessionConsole, text string) {
	t.Helper()
	for !strings.Contains(receive(t, console.messages), text) {
	}
}

func startSession(t *testing.T, session Session, initial, restarted *controlledProcess) (*sessionConsole, context.CancelFunc, <-chan error) {
	t.Helper()
	console := newSessionConsole()
	session.Console = console
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunSession(ctx, session, initial) }()
	t.Cleanup(func() { cancel(); close(console.input); initial.finish(nil); restarted.finish(nil) })
	return console, cancel, done
}

func TestSessionRoutesCommandsAndAdoptsRestartedProcess(t *testing.T) {
	session, initial, restarted := backupSession()
	copied := make(chan string, 1)
	session.Backups = controlledStore{create: func(_ context.Context, _ domain.BackupPlan, message string) error { copied <- message; return nil }}
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "  say hello  "}
	waitCommand(t, initial, "  say hello  ")
	console.input <- commandRead{line: "backup before  пещера 🌍"}
	waitMessage(t, console, "Backup created:")
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "list"}
	waitCommand(t, restarted, "list")
	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	waitCommand(t, restarted, "stop")
	if message := receive(t, copied); message != "before  пещера 🌍" {
		t.Fatalf("message = %q", message)
	}
}

func TestSessionRejectsCommandsWhileBusyAndCancelsOnEOF(t *testing.T) {
	session, initial, restarted := backupSession()
	countdown := make(chan struct{}, 1)
	session.Clock = controlledClock{sleep: func(ctx context.Context, _ time.Duration) error {
		countdown <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup"}
	receive(t, countdown)
	console.input <- commandRead{line: "backup another"}
	console.input <- commandRead{line: "say should be rejected"}
	waitMessage(t, console, "command rejected")
	waitMessage(t, console, "command rejected")
	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	assertNotCompleted(t, launched)
	commands := initial.recorded()
	if len(commands) != 2 || commands[1] != "stop" {
		t.Fatalf("commands = %q", commands)
	}
}

func TestSessionCancellationDuringCopySuppressesRestart(t *testing.T) {
	session, initial, restarted := backupSession()
	copying := make(chan struct{}, 1)
	exitedCopy := make(chan struct{})
	session.Backups = controlledStore{create: func(ctx context.Context, _ domain.BackupPlan, _ string) error {
		copying <- struct{}{}
		<-ctx.Done()
		close(exitedCopy)
		return ctx.Err()
	}}
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	console, cancel, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup"}
	receive(t, copying)
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exitedCopy:
	default:
		t.Fatal("session returned before copy worker exited")
	}
	assertNotCompleted(t, launched)
}

func TestSessionStartupFailureAndCancellationStopOwnedProcess(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "scanner error", "EOF"} {
		t.Run(mode, func(t *testing.T) {
			session, initial, restarted := backupSession()
			initial.ready = make(chan struct{})
			if mode == "timeout" {
				session.WaitTimeout = 20 * time.Millisecond
			}
			console, cancel, done := startSession(t, session, initial, restarted)
			waitMessage(t, console, "Waiting for Minecraft")
			switch mode {
			case "cancel":
				cancel()
			case "EOF":
				console.input <- commandRead{err: io.EOF}
			case "scanner error":
				console.input <- commandRead{err: errors.New("scanner failed")}
			}
			err := receive(t, done)
			if (mode == "timeout" || mode == "scanner error") && err == nil {
				t.Fatal("expected failure")
			}
			if (mode == "cancel" || mode == "EOF") && err != nil {
				t.Fatal(err)
			}
			waitCommand(t, initial, "stop")
		})
	}
}

func TestCancellationDuringRestartStopsNewProcess(t *testing.T) {
	session, initial, restarted := backupSession()
	restarted.ready = make(chan struct{})
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	console, cancel, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup"}
	receive(t, launched)
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	waitCommand(t, restarted, "stop")
}
