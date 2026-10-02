package usecases

import (
	"fmt"

	"game-server-platform/internal/domain"
)

type findBackup struct {
	backupsReader BackupsReader
	pathToBackups string
}

func newFindBackup(
	backupsReader BackupsReader,
	pathToBackups string,
) *findBackup {
	return &findBackup{
		backupsReader: backupsReader,
		pathToBackups: pathToBackups,
	}
}

func (use *findBackup) findBackupName(hash string) (string, error) {
	backupsData, err := use.backupsReader.ReadBackups(use.pathToBackups)
	if err != nil {
		return "", err
	}

	for _, backupData := range backupsData {
		if domain.BackupHash(backupData) == hash {
			return backupData.FileName, nil
		}
	}
	return "", fmt.Errorf("backup hash was not found")
}
