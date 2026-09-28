package domain

import "context"

const (
	Backups = "backups"
)

type BackupPlan struct {
	ServerDirectory string
	WorldDirectory  string
	WorldName       string
	Destination     string
	CreatedAt       string
	Version         string
}

type BackupMetadata struct {
	Game      string `json:"game"`
	Version   string `json:"version"`
	World     string `json:"world"`
	CreatedAt string `json:"created_at"`
}

// OutputMatcher consumes one complete log line. It must not call process methods.
type OutputMatcher func(string) (bool, error)

type ServerProcess interface {
	Send(command string) error
	SendAndWait(context.Context, string, OutputMatcher, string) error
	WaitFor(context.Context, OutputMatcher, string) error
	Done() <-chan struct{}
	Wait() error
}

type ProcessLauncher interface {
	Launch(context.Context, JavaInstallation, string) (ServerProcess, error)
}
