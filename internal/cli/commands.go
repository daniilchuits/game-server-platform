package cli

import (
	"encoding/hex"
	"strings"
)

const BackupUsage = "Usage: backup create [message] | backup use <hash>"

type CommandKind int

const (
	EmptyCommand CommandKind = iota
	ServerCommand
	BackupCommand
	LogsCommand
	UseBackupCommand
	InvalidCommand
)

type Command struct {
	Kind    CommandKind
	Raw     string
	Message string
}

func ParseCommand(line string) Command {
	fullCommand := strings.TrimSpace(line)
	parts := strings.Fields(fullCommand)
	if len(parts) == 0 {
		return Command{Kind: EmptyCommand}
	}

	switch parts[0] {
	case "backup":
		if len(parts) < 2 {
			return invalidBackupCommand()
		}
		arguments := strings.TrimSpace(strings.TrimPrefix(fullCommand, parts[0]))
		subcommand := parts[1]
		remaining := strings.TrimSpace(strings.TrimPrefix(arguments, subcommand))
		switch subcommand {
		case "create":
			return Command{Kind: BackupCommand, Message: remaining}
		case "use":
			if len(parts) != 3 || !validSHA256(parts[2]) {
				return invalidBackupCommand()
			}
			return Command{Kind: UseBackupCommand, Message: strings.ToLower(parts[2])}
		}
		return invalidBackupCommand()
	case "logs":
		if len(parts) > 1 {
			return Command{Kind: EmptyCommand}
		}
		return Command{Kind: LogsCommand}
	}
	return Command{Kind: ServerCommand, Raw: line}
}

func invalidBackupCommand() Command {
	return Command{Kind: InvalidCommand, Message: BackupUsage}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
