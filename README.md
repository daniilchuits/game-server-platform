# GAME-SERVICE-PLATFORM

Platform for locally turning the game-server on 

## CURRENT STATUS

Early development

Implemented so far(only for minectraft):
- Java existecne check
- Java version detection
- User enters the version he needs and the server.jar (minecraft-server-file) downloads localy
- The user accepts the EULA before the first launch; the CLI saves that acceptance and starts the server.
- World backups with an optional message, a save-and-stop sequence, and automatic restart.

Planned:
- Realise using backups 
- Maybe add some more features to minecraft-server, maybe create server-logic to some more games

## REQUIREMENTS

- The Go version declared in `go.mod`
- Java 17+ for Minecraft 1.20.4 (the program checks this before downloading)

## Run 

Clone repository:
```powerShell
git clone git@github.com:daniilchuits/game-server-platform.git
cd game-server-platform
```

Then run this code:
```powerShell
go run .
```

While the server is running, type `backup` to create a world snapshot or
`backup <message>` to include a note. The server announces a ten-second
countdown, flushes the world, stops while the files are copied, and restarts
automatically. Completed snapshots are stored under
`game-server-platform/minecraft/1.20.4/backups/<UTC timestamp>/`.

Each snapshot contains `world/`, `backup.json`, and an optional
`backup_message.txt`. The world comes from `level-name` in `server.properties`.
Commands are rejected while a backup is in progress. Ctrl+C or terminal EOF
requests graceful shutdown and prevents automatic restart.

## TESTS

Run unit tests:
```powerShell
go test ./...
go vet ./...
```

Tests cover Java availability and minimum version errors, terminal routing,
save acknowledgements, process output and exit handling, cancellation, restart
failures, snapshot contents, and unsafe paths (including Windows junctions).
The ordinary suite uses temporary files and helper processes and needs no Java
installation or Minecraft download.

To run the optional real Minecraft check in PowerShell:

```powershell
$env:GSP_MINECRAFT_JAR = (Resolve-Path '.\game-server-platform\minecraft\1.20.4\server.jar').Path
go test ./internal/usecases -run '^TestMinecraftBackupIntegration$' -v -count=1 -timeout=6m
Remove-Item Env:GSP_MINECRAFT_JAR
```

This requires Java 17+ and an existing `eula=true` beside the supplied 1.20.4
jar. It creates a disposable world bound to localhost, changes its spawn,
compares every copied file before restart, and checks status connections before
and after restart. It never opens the existing world. Connecting a player from
a second LAN device remains a separate manual check.

---
Work in progress
