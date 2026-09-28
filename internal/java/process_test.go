package java

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

type commandWriter struct{ write func([]byte) (int, error) }

func (writer commandWriter) Write(data []byte) (int, error) {
	if writer.write != nil {
		return writer.write(data)
	}
	return len(data), nil
}
func (commandWriter) Close() error { return nil }
func exact(text string) domain.OutputMatcher {
	return func(line string) (bool, error) { return line == text, nil }
}

func TestSendAndWaitRegistersBeforeImmediateSplitResponse(t *testing.T) {
	var output bytes.Buffer
	var writer *lineWriter
	process := newProcess(commandWriter{write: func(data []byte) (int, error) {
		if string(data) != "save-all flush\n" {
			t.Errorf("command = %q", data)
		}
		writer.Write([]byte("Saved the "))
		writer.Write([]byte("game\r\n"))
		return len(data), nil
	}})
	writer = &lineWriter{process: process, destination: &output}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.SendAndWait(ctx, "save-all flush", exact("Saved the game"), "save"); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Saved the game\r\n" {
		t.Fatalf("output = %q", output.String())
	}
	if len(process.waiters) != 0 {
		t.Fatal("completed waiter was not removed")
	}
}

func TestSaveWaitIgnoresHistoricalOutputButReadinessReplaysIt(t *testing.T) {
	process := newProcess(commandWriter{})
	process.emit("ready")
	process.emit("Saved the game")
	if err := process.WaitFor(context.Background(), exact("ready"), "ready"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := process.SendAndWait(ctx, "save-all flush", exact("Saved the game"), "save")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale output acknowledged save: %v", err)
	}
	if len(process.waiters) != 0 {
		t.Fatal("timed-out waiter was not removed")
	}
}

func TestWaitForDelayedOutputAndFinalUnterminatedLine(t *testing.T) {
	process := newProcess(commandWriter{})
	waiter := process.register(exact("ready"), false)
	writer := &lineWriter{process: process}
	writer.Write([]byte("re"))
	select {
	case <-waiter.result:
		t.Fatal("partial line accepted")
	default:
	}
	writer.Write([]byte("ady"))
	writer.finish()
	process.complete(nil)
	if err := process.await(context.Background(), waiter, "ready"); err != nil {
		t.Fatal(err)
	}
}

func TestProcessWaitBroadcastsSameResult(t *testing.T) {
	process := newProcess(commandWriter{})
	failure := errors.New("exit status 7")
	const count = 12
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() { results <- process.Wait() }()
	}
	process.complete(failure)
	for i := 0; i < count; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, failure) {
				t.Fatalf("wait = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Wait did not broadcast")
		}
	}
	if err := process.Send("list"); err == nil {
		t.Fatal("command accepted after exit")
	}
	if err := process.WaitFor(context.Background(), exact("never"), "response"); err == nil {
		t.Fatal("wait succeeded after exit without response")
	}
}

func TestLongOutputIsDrainedAndOversizedLinesFailWaiters(t *testing.T) {
	for _, size := range []int{100 * 1024, maxLogLine + 1} {
		t.Run(map[bool]string{true: "over scanner default", false: "over safety limit"}[size < maxLogLine], func(t *testing.T) {
			var output bytes.Buffer
			process := newProcess(commandWriter{})
			writer := &lineWriter{process: process, destination: &output}
			waiter := process.register(exact("ready"), false)
			data := strings.Repeat("x", size) + "\nready\n"
			for start := 0; start < len(data); start += 4096 {
				end := min(start+4096, len(data))
				if n, err := writer.Write([]byte(data[start:end])); err != nil || n != end-start {
					t.Fatalf("write = %d, %v", n, err)
				}
			}
			err := process.await(context.Background(), waiter, "ready")
			if size < maxLogLine && err != nil {
				t.Fatal(err)
			}
			if size > maxLogLine && (err == nil || !strings.Contains(err.Error(), "1 MiB")) {
				t.Fatalf("error = %v", err)
			}
			if output.String() != data {
				t.Fatal("output stopped draining")
			}
			process.complete(nil)
			if size > maxLogLine && process.Wait() == nil {
				t.Fatal("output failure was lost at exit")
			}
		})
	}
}

func TestOutputWriteFailuresReachWaitersWithoutBlockingDrain(t *testing.T) {
	for _, short := range []bool{false, true} {
		process := newProcess(commandWriter{})
		waiter := process.register(exact("ready"), false)
		writer := &lineWriter{process: process, destination: commandWriter{write: func(data []byte) (int, error) {
			if short {
				return len(data) - 1, nil
			}
			return 0, errors.New("terminal unavailable")
		}}}
		if n, err := writer.Write([]byte("ready\n")); err != nil || n != 6 {
			t.Fatalf("drain = %d, %v", n, err)
		}
		if err := process.await(context.Background(), waiter, "ready"); err == nil {
			t.Fatal("output error ignored")
		}
	}
}

func TestSendFailureAndCancelledWaitRemoveListeners(t *testing.T) {
	writes := 0
	process := newProcess(commandWriter{write: func([]byte) (int, error) { writes++; return 0, io.ErrClosedPipe }})
	if err := process.SendAndWait(context.Background(), "save-off", exact("off"), "saving"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("error = %v", err)
	}
	if len(process.waiters) != 0 {
		t.Fatal("failed write leaked waiter")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.SendAndWait(ctx, "stop", exact("stopped"), "stop"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if writes != 1 {
		t.Fatal("cancelled command was sent")
	}
}

func TestConcurrentCommandsStayAsWholeLines(t *testing.T) {
	var commands bytes.Buffer
	process := newProcess(commandWriter{write: commands.Write})
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := process.Send("say hello"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if commands.String() != strings.Repeat("say hello\n", 20) {
		t.Fatalf("commands = %q", commands.String())
	}
	for _, command := range []string{"stop\nsay surprise", "stop\rsay surprise"} {
		if err := process.Send(command); err == nil {
			t.Fatal("multiline command accepted")
		}
	}
}

func TestMatcherErrorFailsImmediately(t *testing.T) {
	process := newProcess(commandWriter{})
	failure := errors.New("world save failed")
	waiter := process.register(func(string) (bool, error) { return false, failure }, false)
	process.emit("save failure")
	if err := process.await(context.Background(), waiter, "save"); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
}
