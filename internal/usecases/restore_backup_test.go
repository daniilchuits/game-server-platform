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

type controlledBackupsReader struct {
	backups []domain.BackupData
	err     error
}

func (reader controlledBackupsReader) ReadBackups(string) ([]domain.BackupData, error) {
	return reader.backups, reader.err
}

type controlledRestorer struct {
	prepareError error
	install      func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error)
}

func (restorer controlledRestorer) PrepareRestore(
	ctx context.Context,
	directory string,
	version string,
	backupName string,
) (domain.RestorePlan, error) {
	if restorer.prepareError != nil {
		return domain.RestorePlan{}, restorer.prepareError
	}
	return domain.RestorePlan{
		ServerDirectory: directory,
		BackupName:      backupName,
		Version:         version,
	}, ctx.Err()
}

func (restorer controlledRestorer) InstallRestore(
	ctx context.Context,
	plan domain.RestorePlan,
) (domain.RestoreTransaction, error) {
	if restorer.install != nil {
		return restorer.install(ctx, plan)
	}
	return &controlledRestoreTransaction{}, ctx.Err()
}

type controlledRestoreTransaction struct {
	commitError   error
	rollbackError error
	committed     chan struct{}
	rolledBack    chan struct{}
}

func (transaction *controlledRestoreTransaction) Commit() error {
	if transaction.committed != nil {
		transaction.committed <- struct{}{}
	}
	return transaction.commitError
}

func (transaction *controlledRestoreTransaction) Rollback() (bool, error) {
	if transaction.rolledBack != nil {
		transaction.rolledBack <- struct{}{}
	}
	return transaction.rollbackError == nil, transaction.rollbackError
}

func TestRestoreBackupCreatesSafetyBackupAndRestarts(t *testing.T) {
	session, initial, restarted := restoreSession()
	transaction := &controlledRestoreTransaction{committed: make(chan struct{}, 1)}
	session.Restorer = controlledRestorer{install: func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error) {
		return transaction, nil
	}}
	safetyMessage := make(chan string, 1)
	session.Backups = controlledStore{create: func(_ context.Context, _ domain.BackupPlan, message string) error {
		safetyMessage <- message
		return nil
	}}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]

	result := RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
	if result.Err != nil || result.Fatal || result.Process != restarted || result.Path != "snapshot" || result.Restored != backup.FileName {
		t.Fatalf("result = %+v", result)
	}
	if got := receive(t, safetyMessage); got != "Automatic safety backup before restoring "+backup.FileName {
		t.Fatalf("safety message = %q", got)
	}
	receive(t, transaction.committed)
	wantCommands := []string{
		"say World restore and restart in 10 seconds. Please reconnect afterward.",
		"say World restore and restart in 5 seconds. Please reconnect afterward.",
		"save-off", "save-all flush", "stop",
	}
	if got := initial.recorded(); strings.Join(got, "|") != strings.Join(wantCommands, "|") {
		t.Fatalf("commands = %q", got)
	}
}

func TestRestoreBackupInvalidHashDoesNotInterruptServer(t *testing.T) {
	session, initial, _ := restoreSession()
	result := RestoreBackup(context.Background(), session, initial, strings.Repeat("0", 64))
	if result.Err == nil || result.Fatal || result.Process != initial {
		t.Fatalf("result = %+v", result)
	}
	if commands := initial.recorded(); len(commands) != 0 {
		t.Fatalf("commands = %q", commands)
	}
}

func TestRestoreBackupPreflightFailureDoesNotInterruptServer(t *testing.T) {
	session, initial, _ := restoreSession()
	failure := errors.New("invalid snapshot")
	session.Restorer = controlledRestorer{prepareError: failure}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]
	result := RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
	if !errors.Is(result.Err, failure) || result.Fatal || result.Process != initial {
		t.Fatalf("result = %+v", result)
	}
	if commands := initial.recorded(); len(commands) != 0 {
		t.Fatalf("commands = %q", commands)
	}
}

func TestRestoreBackupFailuresRestartOriginalWorld(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Session, error)
	}{
		{name: "safety backup", configure: func(session *Session, failure error) {
			session.Backups = controlledStore{create: func(context.Context, domain.BackupPlan, string) error { return failure }}
		}},
		{name: "install", configure: func(session *Session, failure error) {
			session.Restorer = controlledRestorer{install: func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error) {
				return nil, failure
			}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, initial, restarted := restoreSession()
			failure := errors.New("operation failed")
			test.configure(&session, failure)
			backup := session.BackupsReader.(controlledBackupsReader).backups[0]
			result := RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
			if !errors.Is(result.Err, failure) || result.Fatal || result.Process != restarted || result.Restored != "" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestRestoreBackupRollsBackWhenRestoredServerCannotStart(t *testing.T) {
	session, initial, _ := restoreSession()
	failedStart := newControlledProcess()
	failedStart.ready = make(chan struct{})
	recovered := newControlledProcess()
	launches := 0
	session.Process = &controlledLauncher{launch: func() (ServerProcess, error) {
		launches++
		if launches == 1 {
			return failedStart, nil
		}
		return recovered, nil
	}}
	session.WaitTimeout = 20 * time.Millisecond
	transaction := &controlledRestoreTransaction{rolledBack: make(chan struct{}, 1)}
	session.Restorer = controlledRestorer{install: func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error) {
		return transaction, nil
	}}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]

	result := RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
	if result.Err == nil || result.Fatal || result.Process != recovered || launches != 2 || result.Restored != "" {
		t.Fatalf("result = %+v, launches = %d", result, launches)
	}
	receive(t, transaction.rolledBack)
	if got := failedStart.recorded(); len(got) == 0 || got[len(got)-1] != "stop" {
		t.Fatalf("failed process commands = %q", got)
	}
}

func TestRestoreBackupRollbackFailureIsFatal(t *testing.T) {
	session, initial, _ := restoreSession()
	failedStart := newControlledProcess()
	failedStart.ready = make(chan struct{})
	launches := 0
	session.Process = &controlledLauncher{launch: func() (ServerProcess, error) {
		launches++
		return failedStart, nil
	}}
	session.WaitTimeout = 20 * time.Millisecond
	rollbackFailure := errors.New("rollback failed")
	session.Restorer = controlledRestorer{install: func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error) {
		return &controlledRestoreTransaction{rollbackError: rollbackFailure}, nil
	}}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]

	result := RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
	if !errors.Is(result.Err, rollbackFailure) || !result.Fatal || launches != 1 {
		t.Fatalf("result = %+v, launches = %d", result, launches)
	}
}

func TestRestoreBackupCancellationAfterSwapRollsBack(t *testing.T) {
	session, initial, restarted := restoreSession()
	restarted.ready = make(chan struct{})
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	transaction := &controlledRestoreTransaction{rolledBack: make(chan struct{}, 1)}
	session.Restorer = controlledRestorer{install: func(context.Context, domain.RestorePlan) (domain.RestoreTransaction, error) {
		return transaction, nil
	}}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan BackupResult, 1)
	go func() { done <- RestoreBackup(ctx, session, initial, domain.BackupHash(backup)) }()
	receive(t, launched)
	cancel()
	result := receive(t, done)
	if !errors.Is(result.Err, context.Canceled) || result.Fatal || result.Restored != "" {
		t.Fatalf("result = %+v", result)
	}
	receive(t, transaction.rolledBack)
	if got := restarted.recorded(); len(got) == 0 || got[len(got)-1] != "stop" {
		t.Fatalf("restarted process commands = %q", got)
	}
}

func TestRestoreBackupShutdownTimeoutAbandonsRestoreAndRestarts(t *testing.T) {
	session, initial, restarted := restoreSession()
	initial.stopAutomatically = false
	session.WaitTimeout = 20 * time.Millisecond
	stalled := make(chan struct{}, 1)
	session.progress = func(state domain.SessionState) {
		if strings.Contains(string(state), "deadline exceeded") {
			stalled <- struct{}{}
		}
	}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]
	done := make(chan BackupResult, 1)
	go func() {
		done <- RestoreBackup(context.Background(), session, initial, domain.BackupHash(backup))
	}()
	receive(t, stalled)
	assertNotCompleted(t, done)
	initial.finish(nil)
	result := receive(t, done)
	if result.Err == nil || result.Fatal || result.Process != restarted || result.Path != "" || result.Restored != "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSessionSupervisesRestoreAndRejectsBusyCommands(t *testing.T) {
	session, initial, restarted := restoreSession()
	installing := make(chan struct{}, 1)
	release := make(chan struct{})
	transaction := &controlledRestoreTransaction{committed: make(chan struct{}, 1)}
	session.Restorer = controlledRestorer{install: func(ctx context.Context, _ domain.RestorePlan) (domain.RestoreTransaction, error) {
		installing <- struct{}{}
		select {
		case <-release:
			return transaction, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup use " + domain.BackupHash(backup)}
	receive(t, installing)
	console.input <- commandRead{line: "say must be rejected"}
	waitMessage(t, console, "command rejected")
	close(release)
	waitMessage(t, console, "Backup restored: "+backup.FileName)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "list"}
	waitCommand(t, restarted, "list")
	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	waitCommand(t, restarted, "stop")
	receive(t, transaction.committed)
	if strings.Contains(strings.Join(initial.recorded(), "|"), "must be rejected") {
		t.Fatal("busy command reached the old server")
	}
}

func TestSessionInvalidBackupCommandPrintsUsage(t *testing.T) {
	session, initial, restarted := restoreSession()
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup"}
	waitMessage(t, console, "Usage: backup create [message] | backup use <hash>")
	if commands := initial.recorded(); len(commands) != 0 {
		t.Fatalf("invalid command reached Java: %q", commands)
	}
	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSessionEOFDuringRestoreSuppressesRestart(t *testing.T) {
	session, initial, restarted := restoreSession()
	installing := make(chan struct{}, 1)
	session.Restorer = controlledRestorer{install: func(ctx context.Context, _ domain.RestorePlan) (domain.RestoreTransaction, error) {
		installing <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	launched := make(chan struct{}, 1)
	session.Process.(*controlledLauncher).launched = launched
	backup := session.BackupsReader.(controlledBackupsReader).backups[0]
	console, _, done := startSession(t, session, initial, restarted)
	waitMessage(t, console, "Server ready.")
	console.input <- commandRead{line: "backup use " + domain.BackupHash(backup)}
	receive(t, installing)
	console.input <- commandRead{err: io.EOF}
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	assertNotCompleted(t, launched)
}

func restoreSession() (Session, *controlledProcess, *controlledProcess) {
	session, initial, restarted := backupSession()
	backup := domain.BackupData{FileName: "2026-10-02_12-00-00Z", Message: "known good"}
	session.BackupsReader = controlledBackupsReader{backups: []domain.BackupData{backup}}
	session.Restorer = controlledRestorer{}
	return session, initial, restarted
}
