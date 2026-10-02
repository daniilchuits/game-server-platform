package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"game-server-platform/internal/domain"
)

type BackupsReader struct {
	BackupsPath string
}

const (
	backups       = "backups"
	backupMessage = "backup_message.txt"
)

func (b BackupsReader) ReadBackups(currDir string) ([]domain.BackupData, error) {
	path := filepath.Join(currDir, b.BackupsPath, backups)
	infoArr, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var backupsData []domain.BackupData
	for i := len(infoArr) - 1; i >= 0; i-- {
		if !infoArr[i].IsDir() || strings.HasPrefix(infoArr[i].Name(), ".") {
			continue
		}
		fullPath := filepath.Join(path, infoArr[i].Name(), backupMessage)
		message, err := readBackupMessage(fullPath)
		if err != nil {
			return nil, err
		}
		backupsData = append(backupsData, domain.BackupData{
			FileName: infoArr[i].Name(),
			Message:  message,
		})
	}
	return backupsData, nil
}

func readBackupMessage(backupPath string) (string, error) {
	b, err := os.ReadFile(backupPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
