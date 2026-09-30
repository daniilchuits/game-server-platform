package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func HashBackups(msg string) string {

	h := sha256.Sum256([]byte(msg))
	normalHash := hex.EncodeToString(h[:])
	return fmt.Sprintf("%sHash: %s\n", msg, normalHash)
}
