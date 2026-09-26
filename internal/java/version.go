package java

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`(?m)^\s*(?:openjdk|java)\s+(?:version\s+)?"?([0-9]+(?:\.[0-9]+)*)`)

// parseMajorVersion understands modern versions (17.0.12) and legacy ones (1.8.0).
func parseMajorVersion(output string) (int, error) {
	match := versionPattern.FindStringSubmatch(output)
	if len(match) < 2 {
		return 0, fmt.Errorf("cannot recognize the Java version in java -version output")
	}
	parts := strings.Split(match[1], ".")
	major := parts[0]
	if major == "1" && len(parts) > 1 {
		major = parts[1]
	}
	value, err := strconv.Atoi(major)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid Java major version %q", major)
	}
	return value, nil
}
