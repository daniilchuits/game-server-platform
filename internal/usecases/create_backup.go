package usecases

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"game-server-platform/internal/domain"
	"game-server-platform/internal/minecraft"
)

// BackupResult always retains ownership of the current process, even on failure.
type BackupResult struct {
	Process ServerProcess
	Path    string
	Err     error
	Fatal   bool
}

func (session Session) report(state domain.SessionState) {
	if session.progress != nil {
		session.progress(state)
	}
}

func CreateBackup(ctx context.Context, session Session, process ServerProcess, message string) BackupResult {
	result := BackupResult{Process: process}
	if session.Clock == nil {
		session.Clock = RealClock{}
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	select {
	case <-process.Done():
		result.Err = errors.Join(errors.New("cannot back up an exited server"), process.Wait())
		result.Fatal = true
		return result
	default:
	}
	if err := waitReady(ctx, session, process); err != nil {
		result.Err = fmt.Errorf("backup requires a ready server: %w", err)
		return result
	}
	plan, err := session.Backups.Prepare(ctx, session.Directory, session.Version, session.Clock.Now())
	if err != nil {
		result.Err = err
		return result
	}
	session.report(domain.Countdown)
	for _, seconds := range []int{10, 5} {
		if err := process.Send(fmt.Sprintf("say Backup and restart in %d seconds. Please reconnect afterward.", seconds)); err != nil {
			result.Err = err
			return result
		}
		if err := session.Clock.Sleep(ctx, 5*time.Second); err != nil {
			result.Err = err
			return result
		}
	}
	session.report(domain.Saving)
	var alreadyOff atomic.Bool
	matchOff := minecraft.Response(func(line string) bool {
		if minecraft.ContainsResponse(line, "Saving is already turned off") {
			alreadyOff.Store(true)
		}
		return minecraft.SaveOff(line)
	})
	if err := sendAndWait(ctx, session, process, "save-off", matchOff); err != nil {
		return recoverSaving(ctx, session, process, err, !alreadyOff.Load())
	}
	if err := sendAndWait(ctx, session, process, "save-all flush", minecraft.Response(minecraft.SaveComplete)); err != nil {
		return recoverSaving(ctx, session, process, err, !alreadyOff.Load())
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	session.report(domain.Stopping)
	if err := process.Send("stop"); err != nil {
		return recoverSaving(ctx, session, process, err, !alreadyOff.Load())
	}
	stopContext, cancel := context.WithTimeout(ctx, session.timeout())
	err = waitProcess(stopContext, process)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		// A late clean exit permits a restart, but this timed-out backup is discarded.
		result.Err = errors.New("backup abandoned: server shutdown exceeded its deadline; waiting for exit")
		session.report(domain.SessionState("stopping (deadline exceeded; still waiting for Java to exit)"))
		if lateErr := waitProcess(ctx, process); lateErr != nil {
			result.Err = errors.Join(result.Err, lateErr)
			result.Fatal = true
			return result
		}
		return restartAfterBackup(ctx, session, result)
	}
	if err != nil {
		result.Err = err
		result.Fatal = true
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	session.report(domain.Copying)
	if err := session.Backups.Create(ctx, plan, message); err != nil {
		result.Err = fmt.Errorf("create backup: %w", err)
	} else {
		result.Path = plan.Destination
	}
	return restartAfterBackup(ctx, session, result)
}

func sendAndWait(ctx context.Context, session Session, process ServerProcess, command string, match domain.OutputMatcher) error {
	wait, cancel := context.WithTimeout(ctx, session.timeout())
	defer cancel()
	return process.SendAndWait(wait, command, match, command+" acknowledgement")
}

func recoverSaving(ctx context.Context, session Session, process ServerProcess, failure error, restore bool) BackupResult {
	result := BackupResult{Process: process, Err: failure}
	// The session will perform a graceful shutdown if its context was cancelled.
	if ctx.Err() != nil {
		return result
	}
	if restore {
		session.report(domain.Recovering)
		if err := sendAndWait(ctx, session, process, "save-on", minecraft.Response(minecraft.SaveOn)); err != nil {
			result.Err = errors.Join(failure, fmt.Errorf("could not confirm automatic saving recovery: %w", err))
			result.Fatal = true
		}
	}
	return result
}

func restartAfterBackup(ctx context.Context, session Session, result BackupResult) BackupResult {
	if err := ctx.Err(); err != nil {
		result.Err = errors.Join(result.Err, err)
		return result
	}
	session.report(domain.Restarting)
	process, err := session.Process.Launch(ctx, session.Installation, session.Directory)
	if process != nil {
		result.Process = process
	}
	if err == nil {
		err = waitReady(ctx, session, process)
	}
	if err != nil {
		result.Err = errors.Join(result.Err, fmt.Errorf("restart server: %w", err))
		result.Fatal = true
	}
	return result
}
