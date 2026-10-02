package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func HashBackups(msg string) string {
	h := sha256.Sum256([]byte(msg))
	return hex.EncodeToString(h[:])
}

func DataAndHash(msg string) string {
	return fmt.Sprintf("%sHash: %s", msg, HashBackups(msg))
}

func BackupDescription(backup BackupData) string {
	if backup.Message != "" {
		return fmt.Sprintf("Backupname: %s Message: %s\n", backup.FileName, backup.Message)
	}
	return fmt.Sprintf("Backupname: %s\n", backup.FileName)
}

func BackupHash(backup BackupData) string {
	return HashBackups(BackupDescription(backup))
}
