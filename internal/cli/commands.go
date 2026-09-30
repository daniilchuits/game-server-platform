package cli

import (
	"strings"
	"unicode"
)

type CommandKind int

const (
	EmptyCommand CommandKind = iota
	ServerCommand
	BackupCommand
	LogsCommand
)

type Command struct {
	Kind    CommandKind
	Raw     string
	Message string
}

func ParseCommand(line string) Command {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Command{Kind: EmptyCommand}
	}
	word, message := trimmed, ""
	if end := strings.IndexFunc(trimmed, unicode.IsSpace); end >= 0 {
		word, message = trimmed[:end], strings.TrimSpace(trimmed[end:])
	}
	switch word {
	case "backup":
		return Command{Kind: BackupCommand, Message: message}
	case "logs": // getting "logs", but nothing is happening
		return Command{Kind: LogsCommand, Message: message}
	}
	return Command{Kind: ServerCommand, Raw: line}
}

// start in storage/read_backups.go
