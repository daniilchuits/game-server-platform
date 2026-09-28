package minecraft

import "testing"

func TestConsoleResponses(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{"save off", "[Server thread/INFO]: Automatic saving is now disabled", true},
		{"save already off", "[Server thread/INFO]: Saving is already turned off", true},
		{"save complete", "[Server thread/INFO]: Saved the game", true},
		{"save failure", "[Server thread/INFO]: Unable to save the game (is there enough disk space?)", true},
		{"chat does not match", "<Alex> Saved the game", false},
		{"old prefix does not match", "Saved the game from yesterday", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matched := SaveOff(test.line) || SaveComplete(test.line) || SaveFailed(test.line)
			if matched != test.match {
				t.Fatalf("matched = %v, want %v", matched, test.match)
			}
		})
	}
}

func TestReadyResponse(t *testing.T) {
	if !Ready("[Server thread/INFO]: Done (3.2s)! For help, type \"help\"") {
		t.Fatal("ready response not recognized")
	}
	if Ready("Done with backup") {
		t.Fatal("unrelated output recognized as ready")
	}
}

func TestPlayerChatCannotAcknowledgeSaveOrReadiness(t *testing.T) {
	for _, line := range []string{
		"[12:30:00] [Server thread/INFO]: <Alex> ]: Saved the game",
		"[12:30:00] [Server thread/INFO]: [Alex]: Saved the game",
		"[12:30:00] [Server thread/INFO]: <Alex> ]: Done (1s)! For help, type \"help\"",
	} {
		if SaveComplete(line) || Ready(line) {
			t.Errorf("accepted chat as a server response: %s", line)
		}
	}
}

func TestMinecraftResponseRejectsPartialSavesAndRecognizesErrors(t *testing.T) {
	match := Response(SaveComplete)
	for _, line := range []string{
		"[12:30:00] [Server thread/INFO]: <Alex> Unable to save the game (is there enough disk space?)",
		"[12:30:00] [Server thread/INFO]: Saving chunks for level 'ServerLevel[world]'/minecraft:overworld",
		"[12:30:00] [Server thread/INFO]: ThreadedAnvilChunkStorage: All dimensions are saved",
		"[12:30:00] [Worker-Main-1/INFO]: Saved the game",
		"[12:30:00] [Server thread/WARN]: Saved the game",
		"Saved the game",
	} {
		if ok, err := match(line); ok || err != nil {
			t.Errorf("unrelated response accepted: %q => %v, %v", line, ok, err)
		}
	}
	for _, line := range []string{
		"[12:30:00] [Server thread/INFO]: Unable to save the game (is there enough disk space?)",
		"[12:30:00] [Server thread/ERROR]: Failed to save chunk",
		"[12:30:00] [Server thread/ERROR]: Exception closing the level world",
		"[12:30:00] [Server thread/ERROR]: Couldn't save chunks",
	} {
		if ok, err := match(line); ok || err == nil {
			t.Errorf("save error ignored: %q => %v, %v", line, ok, err)
		}
	}
	for _, message := range []string{"Automatic saving is now enabled", "Saving is already turned on"} {
		if !SaveOn("[12:30:00] [Server thread/INFO]: " + message) {
			t.Errorf("save-on not recognized: %s", message)
		}
	}
}
