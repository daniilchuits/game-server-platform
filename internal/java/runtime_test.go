package java

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

// Reuse the test executable as a child process, avoiding a real Java dependency.
func TestMain(m *testing.M) {
	switch os.Getenv("GSP_TEST_JAVA_PROCESS") {
	case "version":
		if strings.Join(os.Args[1:], " ") != "-version" {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "openjdk version \"17.0.12\"")
		os.Exit(0)
	case "server", "failure", "noisy":
		if strings.Join(os.Args[1:], " ") != "-jar server.jar nogui" {
			os.Exit(3)
		}
		if os.Getenv("GSP_TEST_JAVA_PROCESS") == "failure" {
			os.Exit(4)
		}

		go func() { time.Sleep(10 * time.Second); os.Exit(5) }()

		directory, _ := os.Getwd()
		fmt.Fprintln(os.Stdout, "directory="+directory)
		fmt.Fprintln(os.Stderr, "server diagnostics")
		if os.Getenv("GSP_TEST_JAVA_PROCESS") == "noisy" {
			fmt.Fprintln(os.Stdout, strings.Repeat("x", 100*1024))
			for i := 0; i < 200; i++ {
				fmt.Fprintf(os.Stderr, "diagnostic %d\n", i)
			}
		}
		fmt.Fprintln(os.Stdout, "ready")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "stop" {
				fmt.Fprintln(os.Stdout, "world saved")
				os.Exit(0)
			}
		}
		// Did final check of scanner.Err, but no need in that check here
		os.Exit(6)
	}
	os.Exit(m.Run())
}

func TestLaunchedProcessDrainsOutputBeforeAllWaitersReturn(t *testing.T) {
	t.Setenv("GSP_TEST_JAVA_PROCESS", "noisy")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	runtime := New(bufio.NewReader(strings.NewReader("")), &output, &diagnostics)
	process, err := runtime.Launch(context.Background(), domain.JavaInstallation{Path: executable}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { process.Send("stop"); process.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := process.WaitFor(ctx, exact("ready"), "ready"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- process.Wait() }()
	}
	if err := process.Send("stop"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !strings.Contains(output.String(), strings.Repeat("x", 100*1024)+"\n") || !strings.HasSuffix(output.String(), "world saved\n") {
		t.Fatal("stdout was truncated before process completion")
	}
	if !strings.Contains(diagnostics.String(), "diagnostic 199\n") {
		t.Fatal("stderr was truncated")
	}
}

func TestLaunchReportsMissingExecutableAndCancelledContext(t *testing.T) {
	runtime := New(bufio.NewReader(strings.NewReader("")), io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.Launch(ctx, domain.JavaInstallation{Path: "java"}, t.TempDir()); err != context.Canceled {
		t.Fatalf("cancelled launch = %v", err)
	}
	if _, err := runtime.Launch(context.Background(), domain.JavaInstallation{Path: t.TempDir() + "/missing-java"}, t.TempDir()); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestRealVersionCommandReadsStderr(t *testing.T) {
	t.Setenv("GSP_TEST_JAVA_PROCESS", "version")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	check := newChecker()
	check.lookPath = func(string) (string, error) { return executable, nil }
	installation, err := check.Check(context.Background(), 17)
	if err != nil {
		t.Fatal(err)
	}
	if installation.MajorVersion != 17 {
		t.Fatalf("version = %d", installation.MajorVersion)
	}
}

func TestServerOutputAndStopCommand(t *testing.T) {
	t.Setenv("GSP_TEST_JAVA_PROCESS", "server")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	var output, diagnostics bytes.Buffer
	runtime := New(bufio.NewReader(strings.NewReader("stop\n")), &output, &diagnostics)
	if err := runtime.Start(context.Background(), domain.JavaInstallation{Path: executable}, directory); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "directory="+directory) || !strings.Contains(output.String(), "world saved") {
		t.Fatalf("unexpected server output: %s", &output)
	}
	if !strings.Contains(diagnostics.String(), "server diagnostics") {
		t.Fatalf("stderr was not forwarded: %s", &diagnostics)
	}
}

func TestServerCancellationSendsStop(t *testing.T) {
	t.Setenv("GSP_TEST_JAVA_PROCESS", "server")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &readyWriter{cancel: cancel}
	var diagnostics bytes.Buffer
	input, inputWriter := io.Pipe()
	defer input.Close()
	defer inputWriter.Close()
	runtime := New(bufio.NewReader(input), output, &diagnostics)
	if err := runtime.Start(ctx, domain.JavaInstallation{Path: executable}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "world saved") {
		t.Fatalf("server did not save on cancellation: %s", output.String())
	}
}

// Cancellation writes a status message while the child may still be logging.
type readyWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (writer *readyWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	n, err := writer.buffer.Write(data)
	if strings.Contains(writer.buffer.String(), "ready") {
		writer.cancel()
	}
	return n, err
}

func (writer *readyWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buffer.String()
}

func TestServerProcessFailure(t *testing.T) {
	t.Setenv("GSP_TEST_JAVA_PROCESS", "failure")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	runtime := New(bufio.NewReader(strings.NewReader("")), &output, &output)
	err = runtime.Start(context.Background(), domain.JavaInstallation{Path: executable}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "server process failed") {
		t.Fatalf("expected process failure, got %v", err)
	}
}
