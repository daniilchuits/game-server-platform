package usecases

import (
	"fmt"
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

		var msg string
		if backupData.Message != "" {
			msg = fmt.Sprintf(
				"Backupname: %s Message: %s\n",
				backupData.FileName, backupData.Message,
			)
			l.console.Message(domain.HashBackups(msg))
		} else {
			msg = fmt.Sprintf(
				"Backupname: %s\n",
				backupData.FileName,
			)
			l.console.Message(domain.HashBackups(msg))
		}
	}
	// now need to hash this messages
	return nil

	// hashes := domain.HashBackups(backupsNames)
	// for i := range hashes{
	// 	l.Console.Message(fmt.Sprintf(
	// 		"Backup: %"
	// 	))
	// }

}

// start in storage/read_backups.go
