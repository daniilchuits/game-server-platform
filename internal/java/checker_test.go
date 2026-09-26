package java

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckJavaVersions(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		major     int
		wantError string
	}{
		{name: "OpenJDK 17", output: "openjdk version \"17.0.12\" 2024-07-16\nOpenJDK Runtime Environment", major: 17},
		{name: "Oracle 17", output: "java version \"17.0.8\" 2023-07-18 LTS", major: 17},
		{name: "newer Java", output: "openjdk version \"21.0.4\" 2024-07-16", major: 21},
		{name: "unquoted version", output: "openjdk 17.0.12 2024-07-16", major: 17},
		{name: "warning before version", output: "Picked up JAVA_TOOL_OPTIONS: -Xmx1g\nopenjdk version \"17\"", major: 17},
		{name: "Java 8", output: "java version \"1.8.0_421\"", wantError: "Java 8 is too old"},
		{name: "Java 16", output: "openjdk version \"16.0.2\"", wantError: "Java 16 is too old"},
		{name: "unknown output", output: "not a Java runtime", wantError: "cannot recognize"},
		{name: "empty output", wantError: "cannot recognize"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			check := checker{
				lookPath: func(name string) (string, error) {
					if name != "java" {
						t.Fatalf("executable = %q", name)
					}
					return filepath.Join("runtime", "java"), nil
				},
				versionOutput: func(ctx context.Context, path string) ([]byte, error) {
					if !filepath.IsAbs(path) {
						t.Fatalf("Java path is not absolute: %q", path)
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("version check has no timeout")
					}
					return []byte(test.output), nil
				},
			}
			installation, err := check.Check(context.Background(), 17)
			if test.wantError != "" {
				assertJavaError(t, err, test.wantError)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if installation.MajorVersion != test.major {
				t.Fatalf("major version = %d, want %d", installation.MajorVersion, test.major)
			}
		})
	}
}

func TestCheckJavaMissingFromPATH(t *testing.T) {
	// Exercise the real executable lookup, independently of the machine's Java installation.
	t.Setenv("PATH", t.TempDir())
	_, err := newChecker().Check(context.Background(), 17)
	assertJavaError(t, err, "Java was not found on PATH")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("expected executable-not-found error, got %v", err)
	}
}

func TestCheckJavaCannotRun(t *testing.T) {
	failure := errors.New("executable is broken")
	check := checker{
		lookPath:      func(string) (string, error) { return "java", nil },
		versionOutput: func(context.Context, string) ([]byte, error) { return nil, failure },
	}
	_, err := check.Check(context.Background(), 17)
	assertJavaError(t, err, "cannot run java -version")
	if !errors.Is(err, failure) {
		t.Fatalf("lost underlying error: %v", err)
	}
}

func TestCheckJavaCancellation(t *testing.T) {
	check := checker{
		lookPath: func(string) (string, error) { return "java", nil },
		versionOutput: func(ctx context.Context, _ string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := check.Check(ctx, 17)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func assertJavaError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected Java error")
	}
	for _, expected := range []string{message, "Java 17", "download and install", "PATH"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}
}
