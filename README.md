# Game Server Platform

A Go CLI for setting up and running a local Minecraft server with safe world
backup and restore operations.

The project is in early development and currently supports Minecraft 1.20.4.

## Features

- Checks that Java is installed and reports the minimum required version.
- Downloads and verifies the Minecraft server JAR for the selected version.
- Guides the user through Minecraft EULA acceptance.
- Configures the server for offline/LAN play.
- Forwards ordinary console commands directly to Minecraft.
- Creates timestamped world backups with optional messages.
- Lists backups with stable SHA-256 restore hashes.
- Restores backups with a permanent safety snapshot and automatic rollback.
- Rejects commands while a backup or restore operation owns the server.
- Shuts down gracefully on Ctrl+C or terminal EOF.

## Requirements

- The Go version declared in `go.mod`.
- Java 17 or newer for Minecraft 1.20.4.

The application checks Java before downloading or starting Minecraft. If Java
is missing or too old, it prints the minimum version that must be installed.

## Run

Clone the repository:

```powershell
git clone git@github.com:daniilchuits/game-server-platform.git
cd game-server-platform
```

Start the application:

```powershell
go run .
```

Press Enter at the version prompt to use the supported default, `1.20.4`. On
the first run, the application asks you to accept the Minecraft EULA before it
starts the server.

```txt
 [!WARNING]
 The server uses `online-mode=false` for LAN play. Anyone who can reach the
 server can connect using an arbitrary player name. Do not expose it directly
 to the public internet.
```

## Console commands

| Command | Description |
| --- | --- |
| `backup create` | Create a backup without a message. |
| `backup create <message>` | Create a backup with a note that helps identify it. |
| `logs` | List completed backups, their messages, and restore hashes. |
| `backup use <hash>` | Restore the backup identified by the hash from `logs`. |
| Any other command | Forward the original line to the Minecraft console. |

Invalid backup commands print:

```text
Usage: backup create [message] | backup use <hash>
```

### Creating a backup

The application performs the following sequence:

1. Validates the world and backup destination before interrupting gameplay.
2. Announces the restart to players at 10 and 5 seconds.
3. Disables automatic saving and waits for `save-all flush` confirmation.
4. Stops Minecraft and waits for Java to exit.
5. Copies the configured `level-name` world into a temporary snapshot.
6. Publishes the completed snapshot atomically and restarts Minecraft.

Backups are stored under:

```text
game-server-platform/minecraft/1.20.4/backups/<UTC timestamp>/
```

Each completed backup contains:

```text
<UTC timestamp>/
├── world/
├── backup.json
└── backup_message.txt  # present only when a message was supplied
```

### Restoring a backup

Run `logs`, copy the required backup's hash, and pass it to
`backup use <hash>`. Before stopping Minecraft, the application validates the
hash, snapshot metadata, Minecraft version, paths, files, symbolic links, and
Windows junctions.

After validation, the restore operation:

1. Announces the restore and restart to connected players.
2. Flushes and stops the running server.
3. Creates a permanent timestamped safety backup of the current world.
4. Copies the selected snapshot into an operation-owned staging directory.
5. Swaps the staged world into the configured `level-name` location.
6. Restarts Minecraft and waits until the server reports that it is ready.

Completed snapshots are never modified during a restore. If copying or
installation fails, the original world remains available and the application
restarts it. If the restored server cannot become ready, the application rolls
back to the original world and makes one automatic restart attempt. The safety
backup is preserved even when restoration fails.

## Tests

Run the standard checks:

```powershell
go test ./...
go vet ./...
```

The standard suite covers Java detection, command routing, save
acknowledgements, process ownership, cancellation, backup creation, safe
restore transactions, rollback failures, path traversal, symbolic links, and
Windows junctions. It uses temporary directories and helper processes, so it
does not require Java or a Minecraft download.

To run the optional real Minecraft backup test in PowerShell:

```powershell
$env:GSP_MINECRAFT_JAR = (Resolve-Path '.\game-server-platform\minecraft\1.20.4\server.jar').Path
go test ./internal/usecases -run '^TestMinecraftBackupIntegration$' -v -count=1 -timeout=6m
Remove-Item Env:GSP_MINECRAFT_JAR
```

This optional test requires Java 17 or newer and an existing `eula=true` beside
the supplied Minecraft 1.20.4 JAR. It creates and modifies only a disposable
world, compares the snapshot contents, and checks server status before and
after the backup restart. It never opens the existing world.
