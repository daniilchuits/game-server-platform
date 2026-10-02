package domain

type SessionState string

const (
	Starting             SessionState = "starting"
	Running              SessionState = "running"
	Preparing            SessionState = "preparing a backup"
	PreparingRestore     SessionState = "preparing a restore"
	Countdown            SessionState = "counting down to backup"
	RestoreCountdown     SessionState = "counting down to restore"
	Saving               SessionState = "saving"
	Stopping             SessionState = "stopping"
	Copying              SessionState = "copying the world"
	Restarting           SessionState = "restarting"
	Recovering           SessionState = "restoring automatic saving"
	UsingBackup          SessionState = "using backup"
	CreatingSafetyBackup SessionState = "creating a safety backup"
	RollingBack          SessionState = "rolling back the failed restore"
)
