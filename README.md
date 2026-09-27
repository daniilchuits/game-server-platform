# GAME-SERVICE-PLATFORM

Platform for locally turning the game-server on 

## CURRENT STATUS

Early development

Implemented so far(only for minectraft):
- Java existecne check
- Java version detection
- User enters the version he needs and the server.jar (minecraft-server-file) downloads localy
- Program turns the server on, files of the server are created, user must agree Minecraft acception (eula.txt). Than the program turns the server on again

Planned:
- Make backups logic for minecraft-server
- Realise using backups 

- Maybe add some more features to minecraft-server, maybe create server-logic to some more games

## REQUIREMENTS

- Go 1.25+
- Java 21+

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

## TESTS

Run unit tests:
```powerShell
go test ./...
```

---
Work in progress