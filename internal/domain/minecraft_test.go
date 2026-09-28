package domain

import (
	"strings"
	"testing"
)

func TestOnlyMinecraft1204IsSupported(t *testing.T) {
	if err := ValidateVersion("1.20.4"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "1.20.5", "1.19.4", "../world"} {
		if err := ValidateVersion(version); err == nil || !strings.Contains(err.Error(), "only 1.20.4 is supported") {
			t.Fatalf("version %q: %v", version, err)
		}
	}
}
