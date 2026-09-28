package storage

import (
	"fmt"
	"strconv"
	"unicode/utf16"
)

// Java Properties escapes non-ASCII names and literal backslashes when saving
// server.properties. Decode them before resolving the world's actual path.
func decodePropertyValue(value string) (string, error) {
	var units []uint16
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		character := runes[i]
		if character == '\\' {
			i++
			if i == len(runes) {
				return "", fmt.Errorf("unfinished property escape")
			}
			character = runes[i]
			switch character {
			case 'u':
				if i+4 >= len(runes) {
					return "", fmt.Errorf("incomplete Unicode escape")
				}
				number, err := strconv.ParseUint(string(runes[i+1:i+5]), 16, 16)
				if err != nil {
					return "", fmt.Errorf("invalid Unicode escape: %w", err)
				}
				units = append(units, uint16(number))
				i += 4
				continue
			case 't':
				character = '\t'
			case 'n':
				character = '\n'
			case 'r':
				character = '\r'
			case 'f':
				character = '\f'
			}
		}
		units = append(units, utf16.Encode([]rune{character})...)
	}
	return string(utf16.Decode(units)), nil
}
