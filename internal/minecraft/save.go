// Package minecraft contains protocol details from the vanilla server console.
package minecraft

import (
	"fmt"
	"regexp"
	"strings"

	"game-server-platform/internal/domain"
)

// Remove exactly the logging envelope. Never search arbitrary chat for a suffix.
var logEnvelope = regexp.MustCompile(`^(?:\[\d{2}:\d{2}:\d{2}\] )?\[Server thread/(INFO|ERROR|WARN)\]: (.*)$`)
var readyMessage = regexp.MustCompile(`^Done \([0-9.,]+s\)! For help, type "help"$`)

func payload(line string) (string, string) {
	match := logEnvelope.FindStringSubmatch(line)
	if match == nil {
		return "", ""
	}
	return match[1], match[2]
}

func ContainsResponse(line, response string) bool {
	level, text := payload(line)
	return level == "INFO" && text == response
}

func SaveOff(line string) bool {
	return ContainsResponse(line, "Automatic saving is now disabled") || ContainsResponse(line, "Saving is already turned off")
}

func SaveOn(line string) bool {
	return ContainsResponse(line, "Automatic saving is now enabled") || ContainsResponse(line, "Saving is already turned on")
}

func SaveComplete(line string) bool { return ContainsResponse(line, "Saved the game") }
func SaveFailed(line string) bool {
	_, text := payload(line)
	return text == "Unable to save the game (is there enough disk space?)"
}

func Ready(line string) bool {
	level, text := payload(line)
	return level == "INFO" && readyMessage.MatchString(text)
}

// Response reports failures as errors so command waits finish promptly.
func Response(success func(string) bool) domain.OutputMatcher {
	return func(line string) (bool, error) {
		level, text := payload(line)
		if SaveFailed(line) || level == "ERROR" && (strings.HasPrefix(text, "Failed to save") ||
			strings.HasPrefix(text, "Exception closing the level") || strings.HasPrefix(text, "Couldn't save")) {
			return false, fmt.Errorf("Minecraft: %s", text)
		}
		return success(line), nil
	}
}
