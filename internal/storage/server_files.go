// Package storage manages the server's local settings.
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"game-server-platform/internal/domain"
)

type ServerFiles struct{}

func (ServerFiles) CreateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create server directory: %w", err)
	}
	return nil
}

func (ServerFiles) WorldDirectory(directory string) (string, error) {
	lines, err := readProperties(filepath.Join(directory, "server.properties"))
	if err != nil {
		return "", err
	}
	world := "world"
	for _, line := range lines {
		key, value := property(line)
		if key == "level-name" && strings.TrimSpace(value) != "" {
			world, err = decodePropertyValue(value)
			if err != nil {
				return "", fmt.Errorf("decode level-name: %w", err)
			}
		}
	}
	clean := filepath.Clean(world)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("invalid level-name %q: world must be inside the server directory", world)
	}
	first, _, _ := strings.Cut(clean, string(os.PathSeparator))
	if strings.EqualFold(first, domain.Backups) {
		return "", fmt.Errorf("invalid level-name %q: world cannot be inside backups", world)
	}
	return filepath.Join(directory, clean), nil
}

// Create backups/
func (ServerFiles) CreateBackups(directory string) error {
	if err := os.MkdirAll(filepath.Join(directory, domain.Backups), 0755); err != nil {
		return fmt.Errorf("create 'backups' folder: %w", err)
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
