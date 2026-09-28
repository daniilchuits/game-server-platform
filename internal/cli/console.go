// Package cli handles terminal prompts and messages.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"game-server-platform/internal/domain"
)

type Console struct {
	input    *bufio.Reader
	commands *bufio.Scanner
	output   io.Writer
	writeMu  sync.Mutex
}

func New(input *bufio.Reader, output io.Writer) *Console {
	return &Console{input: input, output: output}
}

func (console *Console) ReadVersion() (string, error) {
	version, err := console.ask("Minecraft version (" + domain.MinecraftVersion + "): ")
	if err != nil {
		return "", err
	}
	if version == "" {
		version = domain.MinecraftVersion
	}
	return version, nil
}

func (console *Console) ConfirmEULA(path string) (bool, error) {
	console.Message("Read Minecraft's EULA: " + domain.MinecraftEULA)
	console.Message("Acceptance will be saved to: " + path)
	answer, err := console.ask("Do you agree to the Minecraft EULA? (yes/no): ")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "yes", "y":
		return true, nil
	default:
		return false, nil
	}
}

func (console *Console) Message(message string) {
	console.writeMu.Lock()
	defer console.writeMu.Unlock()
	fmt.Fprintln(console.output, message)
}

func (console *Console) ReadCommand() (string, error) {
	if console.commands == nil {
		// The scanner consumes the same reader used by the startup prompts. No
		// second reader is created, so buffered terminal input is preserved.
		console.commands = bufio.NewScanner(console.input)
	}
	if !console.commands.Scan() {
		if err := console.commands.Err(); err != nil {
			return "", fmt.Errorf("read server command: %w", err)
		}
		return "", io.EOF
	}
	return console.commands.Text(), nil
}

func (console *Console) ask(prompt string) (string, error) {
	if _, err := fmt.Fprint(console.output, prompt); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	line, err := console.input.ReadString('\n')
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return "", fmt.Errorf("terminal input ended before an answer was received: %w", err)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read terminal input: %w", err)
	}
	return strings.TrimSpace(line), nil
}
