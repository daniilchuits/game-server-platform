// Package domain defines the server requirements and data shared by the application.
package domain

import "fmt"

const (
	MinecraftVersion   = "1.20.4"
	MinimumJavaVersion = 17
	MinecraftEULA      = "https://aka.ms/MinecraftEULA"
)

func ValidateVersion(version string) error {
	if version != MinecraftVersion {
		return fmt.Errorf("unsupported Minecraft version %q; only %s is supported", version, MinecraftVersion)
	}
	return nil
}

// ServerDownload contains the integrity information published by Mojang.
type ServerDownload struct {
	URL  string
	SHA1 string
	Size int64
}

type JavaInstallation struct {
	Path         string
	MajorVersion int
}
