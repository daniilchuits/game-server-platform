package usecases

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"game-server-platform/internal/cli"
	"game-server-platform/internal/domain"
	"game-server-platform/internal/minecraft"
)

type Session struct {
	Console      CommandConsole
	Process      ProcessLauncher
	Installation domain.JavaInstallation
	Directory    string
	Version      string
	Backups      BackupStore
	Restorer     BackupRestorer
	Clock        Clock
	Observer     SessionObserver
	// WaitTimeout is per operation. Zero uses the production five-minute limit.
	WaitTimeout   time.Duration
	progress      func(domain.SessionState)
	BackupsReader BackupsReader
}

func (session Session) timeout() time.Duration {
	if session.WaitTimeout > 0 {
		return session.WaitTimeout
	}
	return 5 * time.Minute
}

type commandRead struct {
	line string
	err  error
}

// RunSession owns process replacement and joins an active operation before shutdown.
func RunSession(ctx context.Context, session Session, process ServerProcess) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if session.Clock == nil {
		session.Clock = RealClock{}
	}
	commands := make(chan commandRead)
	go func(reads chan<- commandRead) {
		for {
			line, err := session.Console.ReadCommand()
			select {
			case reads <- commandRead{line: line, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}(commands)

	// Apply each state before its operation continues. This also prevents stale
	// progress from arriving after completion and describing the next backup.
	progress := make(chan domain.SessionState)
	session.progress = func(state domain.SessionState) {
		select {
		case progress <- state:
		case <-ctx.Done():
		}
	}
	operations := make(chan BackupResult, 1)
	var state domain.SessionState
	transition := func(next domain.SessionState) {
		state = next
		if session.Observer != nil {
			session.Observer.SessionStateChanged(next)
		}
	}
	transition(domain.Starting)
	busy := true
	session.Console.Message("Waiting for Minecraft to become ready.")
	go func(initial ServerProcess) {
		err := waitReady(ctx, session, initial)
		operations <- BackupResult{Process: initial, Err: err, Fatal: err != nil}
	}(process)
	processDone := process.Done()
	cancelled := ctx.Done()
	shuttingDown := false
	var inputError error

	for {
		select {
		case <-cancelled:
			shuttingDown = true
			cancelled = nil
			commands = nil
			if !busy {
				return errors.Join(inputError, shutdown(session, process))
			}
		case read := <-commands:
			if read.err != nil {
				if !errors.Is(read.err, io.EOF) {
					inputError = read.err
				}
				shuttingDown = true
				commands = nil
				cancelled = nil
				cancel()
				if !busy {
					return errors.Join(inputError, shutdown(session, process))
				}
				continue
			}
			command := cli.ParseCommand(read.line)
			if command.Kind == cli.EmptyCommand {
				continue
			}
			if busy {
				session.Console.Message(fmt.Sprintf("Server is %s; command rejected.", state))
				continue
			}
			if command.Kind == cli.BackupCommand {
				busy = true
				transition(domain.Preparing)
				current := process
				go func() { operations <- CreateBackup(ctx, session, current, command.Message) }()
				continue
			} else if command.Kind == cli.InvalidCommand {
				session.Console.Message(command.Message)
				continue
			} else if command.Kind == cli.LogsCommand {
				bLogger := newBackupsLogger(
					session.BackupsReader,
					session.Console,
					session.Directory,
				)

				if err := bLogger.logBackups(); err != nil {
					session.Console.Message(fmt.Sprintf(
						"Error logging backups: %s\n",
						err.Error(),
					))
				}
				continue
			} else if command.Kind == cli.UseBackupCommand {
				busy = true
				transition(domain.PreparingRestore)
				current := process
				hash := command.Message
				go func() { operations <- RestoreBackup(ctx, session, current, hash) }()
				continue
			}
			if err := process.Send(command.Raw); err != nil {
				cancel()
				return errors.Join(err, shutdown(session, process))
			}
		case update := <-progress:
			transition(update)
			session.Console.Message("Server is " + string(state) + ".")
		case <-processDone:
			// The operation owns an expected exit during backup and its replacement.
			processDone = nil
			if !busy {
				return process.Wait()
			}
		case result := <-operations:
			busy = false
			process = result.Process
			if result.Path != "" {
				session.Console.Message("Backup created: " + result.Path)
			}
			if result.Restored != "" {
				session.Console.Message("Backup restored: " + result.Restored)
			}
			if result.Err != nil {
				session.Console.Message("Operation failed: " + result.Err.Error())
			}
			if shuttingDown || ctx.Err() != nil {
				return errors.Join(inputError, shutdown(session, process))
			}
			if result.Err != nil {
				if result.Fatal {
					cancel()
					return errors.Join(result.Err, shutdown(session, process))
				}
			}
			if process == nil {
				return errors.New("server operation returned no process")
			}
			select {
			case <-process.Done():
				return process.Wait()
			default:
			}
			transition(domain.Running)
			processDone = process.Done()
			session.Console.Message("Server ready.")
		}
	}
}

// shutdown keeps supervising a slow child instead of abandoning or force-killing it.
func shutdown(session Session, process ServerProcess) error {
	if process == nil {
		return nil
	}
	select {
	case <-process.Done():
		return process.Wait()
	default:
	}
	sendErr := process.Send("stop")
	ctx, cancel := context.WithTimeout(context.Background(), session.timeout())
	defer cancel()
	err := waitProcess(ctx, process)
	if errors.Is(err, context.DeadlineExceeded) {
		session.Console.Message("Server shutdown is taking longer than expected; still waiting for Java to exit.")
		err = process.Wait()
	}
	return errors.Join(sendErr, err)
}

func waitReady(ctx context.Context, session Session, process ServerProcess) error {
	wait, cancel := context.WithTimeout(ctx, session.timeout())
	defer cancel()
	return process.WaitFor(wait, minecraft.Response(minecraft.Ready), "server readiness")
}

func waitProcess(ctx context.Context, process ServerProcess) error {
	select {
	case <-process.Done():
		return process.Wait()
	case <-ctx.Done():
		return ctx.Err()
	}
}
