package webui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

func TestControllerRunsCommandsAndStopsCleanly(t *testing.T) {
	commands := make(chan string, 1)
	controller := NewController(context.Background(), domain.MinecraftVersion, func(ctx context.Context, console *Console) error {
		console.Message("startup message")
		console.SessionStateChanged(domain.Running)
		command, err := console.ReadCommand()
		if err != nil {
			return err
		}
		commands <- command
		<-ctx.Done()
		return ctx.Err()
	})

	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, controller, func(snapshot Snapshot) bool {
		return snapshot.Running && snapshot.CanStop && snapshot.CanSendCommands &&
			snapshot.State == string(domain.Running) && len(snapshot.Logs) == 1
	})
	if err := controller.Start(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start error = %v, want %v", err, ErrAlreadyRunning)
	}
	if err := controller.SendCommand("  say hello from browser  "); err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-commands:
		if command != "say hello from browser" {
			t.Fatalf("command = %q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("browser command was not delivered")
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := controller.AnswerEULA(true); !errors.Is(err, ErrEULANotPending) {
		t.Fatalf("EULA answer after Stop error = %v", err)
	}
	if err := controller.Wait(testContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context cancellation", err)
	}
	snapshot := controller.Snapshot(0)
	if snapshot.Running || !snapshot.CanStart || snapshot.State != stateStopped || snapshot.Error != "" {
		t.Fatalf("final snapshot = %+v", snapshot)
	}
}

func TestControllerAllowsStopButRejectsCommandsDuringOperations(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, func(ctx context.Context, console *Console) error {
		console.SessionStateChanged(domain.Stopping)
		<-ctx.Done()
		return ctx.Err()
	})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	snapshot := waitForSnapshot(t, controller, func(snapshot Snapshot) bool {
		return snapshot.State == string(domain.Stopping)
	})
	if !snapshot.CanStop || snapshot.CanSendCommands {
		t.Fatalf("operation capabilities = %+v", snapshot)
	}
	if err := controller.SendCommand("say not now"); !errors.Is(err, ErrServerBusy) {
		t.Fatalf("busy command error = %v, want %v", err, ErrServerBusy)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = controller.Wait(testContext(t))
}

func TestControllerPublishesAndAnswersEULA(t *testing.T) {
	answer := make(chan bool, 1)
	controller := NewController(context.Background(), domain.MinecraftVersion, func(_ context.Context, console *Console) error {
		accepted, err := console.ConfirmEULA("server/eula.txt")
		if err != nil {
			return err
		}
		answer <- accepted
		return nil
	})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, controller, func(snapshot Snapshot) bool {
		return snapshot.EULAPending && snapshot.EULAPath == "server/eula.txt"
	})
	if err := controller.AnswerEULA(true); err != nil {
		t.Fatal(err)
	}
	select {
	case accepted := <-answer:
		if !accepted {
			t.Fatal("EULA answer was not accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("EULA answer was not delivered")
	}
	if err := controller.Wait(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := controller.AnswerEULA(true); !errors.Is(err, ErrEULANotPending) {
		t.Fatalf("late EULA answer error = %v", err)
	}
}

func TestControllerStopUnblocksPendingEULA(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, func(_ context.Context, console *Console) error {
		_, err := console.ConfirmEULA("server/eula.txt")
		return err
	})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, controller, func(snapshot Snapshot) bool { return snapshot.EULAPending })
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Wait(testContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context cancellation", err)
	}
	snapshot := controller.Snapshot(0)
	if snapshot.Running || snapshot.EULAPending || snapshot.State != stateStopped {
		t.Fatalf("final snapshot = %+v", snapshot)
	}
}

func TestControllerValidatesCommands(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, func(ctx context.Context, _ *Console) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err := controller.SendCommand("help"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("command before Start error = %v", err)
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		command string
		want    error
	}{
		{"   ", ErrEmptyCommand},
		{"say first\nsay second", ErrInvalidCommand},
		{strings.Repeat("x", maxCommandBytes+1), ErrInvalidCommand},
	} {
		if err := controller.SendCommand(test.command); !errors.Is(err, test.want) {
			t.Errorf("SendCommand(%q) error = %v, want %v", test.command, err, test.want)
		}
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	_ = controller.Wait(testContext(t))
}

func TestConsoleWritersCreateCompleteCursorFilteredLines(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, func(_ context.Context, console *Console) error {
		writer := console.Writer("minecraft")
		_, _ = writer.Write([]byte("first\nsec"))
		_, _ = writer.Write([]byte("ond\r\nunterminated"))
		console.Message("application\nmessage")
		return nil
	})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Wait(testContext(t)); err != nil {
		t.Fatal(err)
	}

	snapshot := controller.Snapshot(0)
	want := []string{"first", "second", "application", "message", "unterminated"}
	if len(snapshot.Logs) != len(want) {
		t.Fatalf("log count = %d, want %d: %+v", len(snapshot.Logs), len(want), snapshot.Logs)
	}
	for index, message := range want {
		if snapshot.Logs[index].Message != message {
			t.Errorf("log %d = %q, want %q", index, snapshot.Logs[index].Message, message)
		}
	}
	filtered := controller.Snapshot(snapshot.Logs[1].ID)
	if len(filtered.Logs) != len(want)-2 || filtered.Logs[0].Message != "application" {
		t.Fatalf("filtered logs = %+v", filtered.Logs)
	}
}

func TestConsoleWriterBoundsLongBrowserLogLines(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, func(_ context.Context, console *Console) error {
		writer := console.Writer("minecraft")
		_, _ = writer.Write([]byte(strings.Repeat("x", maxBufferedLogLineBytes+100) + "\n"))
		return nil
	})
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Wait(testContext(t)); err != nil {
		t.Fatal(err)
	}
	logs := controller.Snapshot(0).Logs
	if len(logs) != 1 || !strings.HasSuffix(logs[0].Message, " [truncated]") {
		t.Fatalf("bounded log = %+v", logs)
	}
	if len(logs[0].Message) > maxBufferedLogLineBytes+len(" [truncated]") {
		t.Fatalf("bounded log length = %d", len(logs[0].Message))
	}
}

func waitForSnapshot(t *testing.T, controller *Controller, ready func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := controller.Snapshot(0)
		if ready(snapshot) {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("controller did not reach the expected state: %+v", controller.Snapshot(0))
	return Snapshot{}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}
