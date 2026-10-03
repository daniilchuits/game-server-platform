package usecases

import (
	"context"
	"io"
	"reflect"
	"testing"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/storage"
)

type recordingSessionObserver struct {
	states chan domain.SessionState
}

func newRecordingSessionObserver() *recordingSessionObserver {
	return &recordingSessionObserver{states: make(chan domain.SessionState, 32)}
}

func (observer *recordingSessionObserver) SessionStateChanged(state domain.SessionState) {
	observer.states <- state
}

func TestStartServerPassesObserverToSession(t *testing.T) {
	process := newControlledProcess()
	observer := newRecordingSessionObserver()
	console := newSessionConsole()
	app := StartServer{
		Console: console, Java: setupJava{}, Downloads: setupDownloads{},
		ServerFiles: storage.ServerFiles{}, BaseDirectory: t.TempDir(),
		Process: &controlledLauncher{next: process}, Backups: controlledStore{},
		BackupsReader: controlledBackupsReader{}, Restorer: controlledRestorer{},
		Clock: controlledClock{}, Observer: observer,
	}
	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()

	if got := receive(t, observer.states); got != domain.Starting {
		t.Fatalf("first state = %q, want %q", got, domain.Starting)
	}
	if got := receive(t, observer.states); got != domain.Running {
		t.Fatalf("second state = %q, want %q", got, domain.Running)
	}

	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	waitCommand(t, process, "stop")
}

func TestSessionObserverSeesBackupStateTransitions(t *testing.T) {
	session, initial, restarted := backupSession()
	observer := newRecordingSessionObserver()
	session.Observer = observer
	console, _, done := startSession(t, session, initial, restarted)

	want := []domain.SessionState{
		domain.Starting,
		domain.Running,
		domain.Preparing,
		domain.Countdown,
		domain.Saving,
		domain.Stopping,
		domain.Copying,
		domain.Restarting,
		domain.Running,
	}
	got := []domain.SessionState{
		receive(t, observer.states),
		receive(t, observer.states),
	}
	console.input <- commandRead{line: "backup create observer test"}
	for len(got) < len(want) {
		got = append(got, receive(t, observer.states))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %q, want %q", got, want)
	}

	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}
