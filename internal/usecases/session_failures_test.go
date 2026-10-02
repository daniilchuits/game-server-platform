package usecases

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

func TestFailedPreflightDoesNotInterruptGameplay(t *testing.T) {
	session, initial, _ := backupSession()
	failure := errors.New("world does not exist")
	session.Backups = controlledStore{prepareError: failure}
	result := CreateBackup(context.Background(), session, initial, "")
	if !errors.Is(result.Err, failure) || result.Fatal || len(initial.recorded()) != 0 {
		t.Fatalf("result = %+v, commands = %q", result, initial.recorded())
	}
}

func TestBackupRequiresReadyRunningProcess(t *testing.T) {
	for _, exited := range []bool{false, true} {
		session, initial, _ := backupSession()
		if exited {
			initial.finish(nil)
		} else {
			initial.ready = make(chan struct{})
			session.WaitTimeout = 20 * time.Millisecond
		}
		result := CreateBackup(context.Background(), session, initial, "")
		if result.Err == nil || len(initial.recorded()) != 0 {
			t.Fatalf("result = %+v, commands = %q", result, initial.recorded())
		}
	}
}

func TestSavingRecoveryWaitsForAcknowledgement(t *testing.T) {
	session, initial, restarted := backupSession()
	initial.ack = func(ctx context.Context, command string) (string, error) {
		switch command {
		case "save-off":
			return "Automatic saving is now disabled", nil
		case "save-all flush":
			return "", errors.New("save failed")
		case "save-on":
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "", errors.New("unexpected command")
	}
	console, cancel, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup create"}
	waitCommand(t, initial, "save-on")
	console.input <- commandRead{line: "backup create second"}
	waitMessage(t, console, "command rejected")
	assertNotCompleted(t, done)
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestBusyCommandsAndEOFDuringSaveCopyAndRestart(t *testing.T) {
	for _, stage := range []domain.SessionState{domain.Saving, domain.Copying, domain.Restarting} {
		t.Run(string(stage), func(t *testing.T) {
			session, initial, restarted := backupSession()
			switch stage {
			case domain.Saving:
				initial.ack = func(ctx context.Context, command string) (string, error) {
					if command == "save-off" {
						return "Automatic saving is now disabled", nil
					}
					<-ctx.Done()
					return "", ctx.Err()
				}
			case domain.Copying:
				session.Backups = controlledStore{create: func(ctx context.Context, _ domain.BackupPlan, _ string) error { <-ctx.Done(); return ctx.Err() }}
			case domain.Restarting:
				restarted.ready = make(chan struct{})
			}
			console, _, done := startSession(t, session, initial, restarted)
			waitMessage(t, console, "Server ready.")
			console.input <- commandRead{line: "backup create"}
			waitMessage(t, console, "Server is "+string(stage)+".")
			console.input <- commandRead{line: "backup create again"}
			console.input <- commandRead{line: "say must not reach Java"}
			waitMessage(t, console, "command rejected")
			waitMessage(t, console, "command rejected")
			console.input <- commandRead{err: io.EOF}
			if err := receive(t, done); err != nil {
				t.Fatal(err)
			}
			for _, process := range []*controlledProcess{initial, restarted} {
				if strings.Contains(strings.Join(process.recorded(), "|"), "must not reach Java") {
					t.Fatal("busy command forwarded")
				}
			}
			if stage == domain.Restarting {
				waitCommand(t, restarted, "stop")
			}
		})
	}
}

func TestSessionRestartTimeoutStopsNewProcessAndReportsSnapshot(t *testing.T) {
	session, initial, restarted := backupSession()
	restarted.ready = make(chan struct{})
	session.WaitTimeout = 20 * time.Millisecond
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup create"}
	waitMessage(t, console, "Backup created: snapshot")
	err := receive(t, done)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restart timeout = %v", err)
	}
	waitCommand(t, restarted, "stop")
}

func TestGracefulShutdownWaitsBeyondDeadline(t *testing.T) {
	session, initial, restarted := backupSession()
	session.WaitTimeout = 20 * time.Millisecond
	initial.stopAutomatically = false
	console, cancel, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	cancel()
	waitMessage(t, console, "still waiting for Java to exit")
	assertNotCompleted(t, done)
	initial.finish(nil)
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRealClockObservesCancellation(t *testing.T) {
	clock := RealClock{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := clock.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep = %v", err)
	}
	if err := clock.Sleep(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if clock.Now().IsZero() {
		t.Fatal("clock returned zero time")
	}
}
