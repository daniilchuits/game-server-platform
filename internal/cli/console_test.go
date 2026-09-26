package cli

import (
	"bufio"
	"bytes"
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
	command, err := input.ReadString('\n')
	if err != nil || command != "stop\n" {
		t.Fatalf("buffered command lost: %q, %v", command, err)
	}
	if !strings.Contains(output.String(), "server/eula.txt") || !strings.Contains(output.String(), "https://aka.ms/MinecraftEULA") {
		t.Fatalf("missing EULA information: %s", &output)
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
