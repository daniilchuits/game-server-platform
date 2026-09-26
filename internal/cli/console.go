// Package cli handles terminal prompts and messages.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"game-server-platform/internal/domain"
)

type Console struct {
	input  *bufio.Reader
	output io.Writer
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
	fmt.Fprintln(console.output, message)
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
