package usecases

import (
	"game-server-platform/internal/domain"
)

type backupsLogger struct {
	backupsReader BackupsReader
	console       Console
	pathToBackups string
}

func newBackupsLogger(
	backupsReader BackupsReader,
	console Console,
	pathToBackups string,
) *backupsLogger {
	return &backupsLogger{
		backupsReader: backupsReader,
		console:       console,
		pathToBackups: pathToBackups,
	}
}

func (l *backupsLogger) logBackups() error {
	backupsData, err := l.backupsReader.ReadBackups(l.pathToBackups)
	if err != nil {
		return err
	}

	for _, backupData := range backupsData {
		description := domain.BackupDescription(backupData)
		l.console.Message(domain.DataAndHash(description))
	}
	return nil
}
