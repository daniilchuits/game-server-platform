package usecases

import (
	"context"
	"time"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/storage"
)

// These interfaces describe the services the startup flow needs.
// Implementations live in the CLI, Java, Mojang, and storage packages.
type Console interface {
	ReadVersion() (string, error)
	ConfirmEULA(path string) (bool, error)
	Message(message string)
}

type JavaRuntime interface {
	Check(ctx context.Context, minimumVersion int) (domain.JavaInstallation, error)
	Start(ctx context.Context, installation domain.JavaInstallation, directory string) error
}

type ServerProcess = domain.ServerProcess
type ProcessLauncher = domain.ProcessLauncher

type Downloads interface {
	Resolve(ctx context.Context, version string) (domain.ServerDownload, error)
	EnsureJar(ctx context.Context, download domain.ServerDownload, destination string) error
}

type ServerFiles interface {
	CreateDirectory(directory string) error
	CreateBackups(directory string) error
	EULAAccepted(directory string) (bool, error)
	AcceptEULA(directory string) error
	ConfigureOffline(directory string) error
}

type BackupStore interface {
	Prepare(ctx context.Context, serverDirectory, version string, now time.Time) (domain.BackupPlan, error)
	Create(ctx context.Context, plan domain.BackupPlan, message string) error
}

type CommandConsole interface {
	Console
	ReadCommand() (string, error)
}

type BackupsReader interface {
	ReadBackups(currDir string) ([]storage.BackupData, error)
}

// start in storage/read_backups.go
