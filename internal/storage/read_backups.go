package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

type BackupsReader struct {
	BackupsPath string
}

type BackupData struct {
	FileName string
	Message  string
}

const (
	backups       = "backups"
	backupMessage = "backup_message.txt"
)

func (b BackupsReader) ReadBackups(currDir string) ([]BackupData, error) {

	path := filepath.Join(currDir, b.BackupsPath, backups)
	infoArr, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var backupsData []BackupData
	for i := len(infoArr) - 1; i >= 0; i-- {

		fullPath := filepath.Join(path, infoArr[i].Name(), backupMessage)
		message, err := readBackupMessage(fullPath)

		if err != nil {
			return nil, err
		}
		backupsData = append(backupsData, BackupData{
			FileName: infoArr[i].Name(),
			Message:  message,
		})
	}
	return backupsData, nil
	// get filenames in array and internal of `backups/<TIMESTAMP>/backup_message.txt`
}

func readBackupMessage(backupPath string) (string, error) {

	info, err := os.OpenFile(backupPath, os.O_RDONLY, 0644)
	if err != nil {

		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	b, err := io.ReadAll(info)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
