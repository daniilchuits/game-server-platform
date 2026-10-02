package cli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestPromptFlowPreservesServerCommands(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("\n YES \nstop\n"))
	var output bytes.Buffer
	console := New(input, &output)
	version, err := console.ReadVersion()
	if err != nil || version != "1.20.4" {
		t.Fatalf("version = %q, error = %v", version, err)
	}
	agreed, err := console.ConfirmEULA("server/eula.txt")
	if err != nil || !agreed {
		t.Fatalf("accepted = %v, error = %v", agreed, err)
	}
	command, err := console.ReadCommand()
	if err != nil || command != "stop" {
		t.Fatalf("buffered command lost: %q, %v", command, err)
	}
	if !strings.Contains(output.String(), "server/eula.txt") || !strings.Contains(output.String(), "https://aka.ms/MinecraftEULA") {
		t.Fatalf("missing EULA information: %s", &output)
	}
}

func TestCommandParsing(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for _, test := range []struct {
		line    string
		kind    CommandKind
		message string
	}{
		{"", EmptyCommand, ""},
		{" \t ", EmptyCommand, ""},
		{"backup", InvalidCommand, BackupUsage},
		{"backup create", BackupCommand, ""},
		{"  backup\tcreate before  пещера 🌍  ", BackupCommand, "before  пещера 🌍"},
		{"backup\u2003create Unicode separator", BackupCommand, "Unicode separator"},
		{"backup use " + hash, UseBackupCommand, hash},
		{"backup use missing", InvalidCommand, BackupUsage},
		{"backup use " + hash + " extra", InvalidCommand, BackupUsage},
		{"backup remove " + hash, InvalidCommand, BackupUsage},
		{"backups", ServerCommand, ""},
		{"backupworld", ServerCommand, ""},
		{"  say hello  ", ServerCommand, ""},
		{"/backup", ServerCommand, ""},
	} {
		t.Run(test.line, func(t *testing.T) {
			command := ParseCommand(test.line)
			if command.Kind != test.kind || command.Message != test.message {
				t.Fatalf("command = %+v", command)
			}
			if command.Kind == ServerCommand && command.Raw != test.line {
				t.Fatal("original command was changed")
			}
		})
	}
}

func TestCommandScannerReportsEOFAndOversizedInput(t *testing.T) {
	console := New(bufio.NewReader(strings.NewReader("  list  \nstop")), io.Discard)
	for _, want := range []string{"  list  ", "stop"} {
		got, err := console.ReadCommand()
		if err != nil || got != want {
			t.Fatalf("command = %q, %v", got, err)
		}
	}
	if _, err := console.ReadCommand(); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF = %v", err)
	}
	console = New(bufio.NewReader(strings.NewReader(strings.Repeat("x", 70*1024))), io.Discard)
	if _, err := console.ReadCommand(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("scanner error = %v", err)
	}
}

func TestConsentRequiresExplicitAnswer(t *testing.T) {
	for _, answer := range []string{"no\n", "\n", "maybe\n", ""} {
		t.Run(answer, func(t *testing.T) {
			var output bytes.Buffer
			console := New(bufio.NewReader(strings.NewReader(answer)), &output)
			accepted, _ := console.ConfirmEULA("eula.txt")
			if accepted {
				t.Fatal("accepted EULA without an explicit yes")
			}
		})
	}
}

func TestVersionEOFIsAnError(t *testing.T) {
	var output bytes.Buffer
	_, err := New(bufio.NewReader(strings.NewReader("")), &output).ReadVersion()
	if err == nil {
		t.Fatal("expected input error")
	}
}
