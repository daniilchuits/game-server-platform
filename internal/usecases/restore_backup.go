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

func RestoreBackup(
	ctx context.Context,
	session Session,
	process ServerProcess,
	hash string,
) BackupResult {
	result := BackupResult{Process: process}
	if session.Clock == nil {
		session.Clock = RealClock{}
	}
	if session.BackupsReader == nil || session.Backups == nil || session.Restorer == nil {
		result.Err = errors.New("backup restore services are not configured")
		result.Fatal = true
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Err = err
		return result
	}
	select {
	case <-process.Done():
		result.Err = errors.Join(errors.New("cannot restore an exited server"), process.Wait())
		result.Fatal = true
		return result
	default:
	}
	if err := waitReady(ctx, session, process); err != nil {
		result.Err = fmt.Errorf("restore requires a ready server: %w", err)
		return result
	}

	finder := newFindBackup(session.BackupsReader, session.Directory)
	backupName, err := finder.findBackupName(hash)
	if err != nil {
		result.Err = fmt.Errorf("find backup: %w", err)
		return result
	}
	restorePlan, err := session.Restorer.PrepareRestore(ctx, session.Directory, session.Version, backupName)
	if err != nil {
		result.Err = fmt.Errorf("prepare restore: %w", err)
		return result
	}
	safetyPlan, err := session.Backups.Prepare(ctx, session.Directory, session.Version, session.Clock.Now())
	if err != nil {
		result.Err = fmt.Errorf("prepare safety backup: %w", err)
		return result
	}

	session.report(domain.RestoreCountdown)
	for _, seconds := range []int{10, 5} {
		message := fmt.Sprintf("say World restore and restart in %d seconds. Please reconnect afterward.", seconds)
		if err := process.Send(message); err != nil {
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

	session.report(domain.Stopping)
	if err := process.Send("stop"); err != nil {
		return recoverSaving(ctx, session, process, err, !alreadyOff.Load())
	}
	stopContext, cancel := context.WithTimeout(ctx, session.timeout())
	err = waitProcess(stopContext, process)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		result.Err = errors.New("restore abandoned: server shutdown exceeded its deadline; waiting for exit")
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

	session.report(domain.CreatingSafetyBackup)
	safetyMessage := fmt.Sprintf("Automatic safety backup before restoring %s", backupName)
	if err := session.Backups.Create(ctx, safetyPlan, safetyMessage); err != nil {
		result.Err = fmt.Errorf("create safety backup: %w", err)
		return restartAfterBackup(ctx, session, result)
	}
	result.Path = safetyPlan.Destination

	session.report(domain.UsingBackup)
	transaction, installErr := session.Restorer.InstallRestore(ctx, restorePlan)
	if installErr != nil {
		result.Err = fmt.Errorf("install backup %s: %w", backupName, installErr)
		if transaction != nil {
			restored, rollbackErr := transaction.Rollback()
			if rollbackErr != nil {
				result.Err = errors.Join(result.Err, fmt.Errorf("rollback failed restore: %w", rollbackErr))
			}
			if !restored {
				result.Fatal = true
				return result
			}
		}
		return restartAfterBackup(ctx, session, result)
	}
	if transaction == nil {
		result.Err = errors.New("restore storage returned no transaction")
		result.Fatal = true
		return result
	}

	restarted := restartAfterBackup(ctx, session, result)
	if restarted.Err == nil && !restarted.Fatal {
		restarted.Restored = backupName
		if err := transaction.Commit(); err != nil {
			restarted.Err = errors.Join(restarted.Err, fmt.Errorf("commit restored world: %w", err))
		}
		return restarted
	}
	return rollbackFailedRestart(ctx, session, transaction, restarted)
}

func rollbackFailedRestart(
	ctx context.Context,
	session Session,
	transaction domain.RestoreTransaction,
	failed BackupResult,
) BackupResult {
	failure := failed.Err
	stopErr := shutdown(session, failed.Process)
	session.report(domain.RollingBack)
	restored, rollbackErr := transaction.Rollback()
	if !restored {
		if rollbackErr == nil {
			rollbackErr = errors.New("rollback did not restore the original world")
		}
		failed.Err = errors.Join(failure, stopErr, fmt.Errorf("rollback restored world: %w", rollbackErr))
		failed.Fatal = true
		return failed
	}
	recovered := BackupResult{
		Process: failed.Process,
		Path:    failed.Path,
		Err:     errors.Join(failure, stopErr, rollbackErr),
	}
	if err := ctx.Err(); err != nil {
		recovered.Err = errors.Join(recovered.Err, err)
		return recovered
	}
	return restartAfterBackup(ctx, session, recovered)
}
