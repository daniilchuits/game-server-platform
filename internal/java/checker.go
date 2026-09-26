// Package java checks the installed runtime and manages the server process.
package java

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"game-server-platform/internal/domain"
)

type checker struct {
	lookPath      func(string) (string, error)
	versionOutput func(context.Context, string) ([]byte, error)
}

func newChecker() checker {
	return checker{
		lookPath: exec.LookPath,
		versionOutput: func(ctx context.Context, path string) ([]byte, error) {
			// Java commonly writes its version to stderr, so capture both streams.
			return exec.CommandContext(ctx, path, "-version").CombinedOutput()
		},
	}
}

func (check checker) Check(ctx context.Context, minimum int) (domain.JavaInstallation, error) {
	guidance := fmt.Sprintf("download and install Java %d or newer, add its bin directory to PATH, then reopen your terminal", minimum)
	path, err := check.lookPath("java")
	if err != nil {
		return domain.JavaInstallation{}, fmt.Errorf("Java was not found on PATH; %s: %w", guidance, err)
	}
	// A relative PATH entry must still work after the server changes directory.
	path, err = filepath.Abs(path)
	if err != nil {
		return domain.JavaInstallation{}, fmt.Errorf("resolve Java executable path: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := check.versionOutput(ctx, path)
	if err != nil {
		return domain.JavaInstallation{}, fmt.Errorf("cannot run java -version (%s); %s: %w", path, guidance, err)
	}
	major, err := parseMajorVersion(string(output))
	if err != nil {
		return domain.JavaInstallation{}, fmt.Errorf("%w; %s", err, guidance)
	}
	if major < minimum {
		return domain.JavaInstallation{}, fmt.Errorf("Java %d is too old (minimum: Java %d); %s", major, minimum, guidance)
	}
	return domain.JavaInstallation{Path: path, MajorVersion: major}, nil
}
