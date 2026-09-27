package java

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
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
	case "server", "failure":
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
	runtime := New(bufio.NewReader(strings.NewReader("")), output, &diagnostics)
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
