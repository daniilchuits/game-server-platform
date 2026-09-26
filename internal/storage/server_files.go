// Package storage manages the server's local settings.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

type ServerFiles struct{}

// Create backups/
func (ServerFiles) CreateBackups() error {

}

func (ServerFiles) CreateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create server directory: %w", err)
	}
	return nil
}

func (ServerFiles) EULAAccepted(directory string) (bool, error) {
	lines, err := readProperties(filepath.Join(directory, "eula.txt"))
	if err != nil {
		return false, err
	}
	accepted := false
	for _, line := range lines {
		key, value := property(line)
		if key == "eula" {
			accepted = value == "true"
		}
	}
	return accepted, nil
}

func (ServerFiles) AcceptEULA(directory string) error {
	return setProperty(filepath.Join(directory, "eula.txt"), "eula", "true")
}

func (ServerFiles) ConfigureOffline(directory string) error {
	return setProperty(filepath.Join(directory, "server.properties"), "online-mode", "false")
}
