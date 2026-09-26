package storage

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func readProperties(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n"), nil
}

// property reads the plain keys used in Minecraft-generated settings files.
func property(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return "", ""
	}
	separator := strings.IndexAny(line, "=: \t")
	if separator < 0 {
		return line, ""
	}
	key := line[:separator]
	value := strings.TrimSpace(line[separator:])
	value = strings.TrimPrefix(strings.TrimPrefix(value, "="), ":")
	return key, strings.TrimSpace(value)
}

func setProperty(path, key, value string) error {
	lines, err := readProperties(path)
	if err != nil {
		return err
	}
	found := false
	for index, line := range lines {
		name, _ := property(line)
		if name == key {
			lines[index] = key + "=" + value
			found = true
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
