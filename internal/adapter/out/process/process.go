// Package process starts the application under test for startup timing.
package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type Runner struct{}

// Start runs command via sh in its own process group so stop can terminate
// the whole tree (e.g. `npm start` → node).
func (Runner) Start(ctx context.Context, command string) (func() error, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func() error {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return nil
	}
	select {
	case err := <-done: // exited immediately: bad command
		if err == nil {
			err = errors.New("process exited before becoming ready")
		}
		return nil, err
	case <-time.After(50 * time.Millisecond):
	}
	return stop, nil
}
