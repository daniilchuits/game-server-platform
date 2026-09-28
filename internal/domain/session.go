package domain

type SessionState string

const (
	Starting   SessionState = "starting"
	Running    SessionState = "running"
	Preparing  SessionState = "preparing a backup"
	Countdown  SessionState = "counting down to backup"
	Saving     SessionState = "saving"
	Stopping   SessionState = "stopping"
	Copying    SessionState = "copying the world"
	Restarting SessionState = "restarting"
	Recovering SessionState = "restoring automatic saving"
)
