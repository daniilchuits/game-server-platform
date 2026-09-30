// Package usecases coordinates server setup without depending on concrete adapters.
package usecases

import (
	"context"
	"fmt"
	"path/filepath"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/storage"
)

type StartServer struct {
	Console       CommandConsole
	Java          JavaRuntime
	Downloads     Downloads
	ServerFiles   ServerFiles
	Process       ProcessLauncher
	Backups       BackupStore
	Clock         Clock
	BaseDirectory string
}

func (app StartServer) Run(ctx context.Context) error {
	version, err := app.Console.ReadVersion()
	if err != nil {
		return err
	}
	if err := domain.ValidateVersion(version); err != nil {
		return err
	}

	// Fail before network requests or file changes if Java cannot run this version.
	installation, err := app.Java.Check(ctx, domain.MinimumJavaVersion)
	if err != nil {
		return fmt.Errorf("Minecraft %s: %w", version, err)
	}
	app.Console.Message(fmt.Sprintf("Using Java %d: %s", installation.MajorVersion, installation.Path))

	download, err := app.Downloads.Resolve(ctx, version)
	if err != nil {
		return err
	}
	directory := filepath.Join(app.BaseDirectory, "minecraft", version)
	if err := app.ServerFiles.CreateDirectory(directory); err != nil {
		return err
	}
	jarPath := filepath.Join(directory, "server.jar")
	app.Console.Message("Preparing server jar: " + jarPath)
	if err := app.Downloads.EnsureJar(ctx, download, jarPath); err != nil {
		return err
	}

	agreed, err := app.ServerFiles.EULAAccepted(directory)
	if err != nil {
		return err
	}
	if !agreed {
		agreed, err = app.Console.ConfirmEULA(filepath.Join(directory, "eula.txt"))
		if err != nil {
			return err
		}
		if !agreed {
			app.Console.Message("EULA was not accepted; server was not started.")
			return nil
		}
		if err := app.ServerFiles.AcceptEULA(directory); err != nil {
			return err
		}
	}

	if err := app.ServerFiles.ConfigureOffline(directory); err != nil {
		return err
	}
	app.Console.Message("Offline mode is enabled: reachable clients can join using arbitrary player names.")
	app.Console.Message("LAN players can connect to this computer's local IP (default port 25565).")
	app.Console.Message("Starting Minecraft " + version + ". Type stop and press Enter to save and shut down.")
	if app.Process == nil || app.Backups == nil {
		return app.Java.Start(ctx, installation, directory)
	}
	process, err := app.Process.Launch(ctx, installation, directory)
	if err != nil {
		return err
	}
	return RunSession(ctx,
		Session{
			Console:       app.Console,
			Process:       app.Process,
			Installation:  installation,
			Directory:     directory,
			Version:       version,
			Backups:       app.Backups,
			Clock:         app.Clock,
			BackupsReader: storage.BackupsReader{},
		}, process)
}
