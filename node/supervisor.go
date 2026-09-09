package node

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// node supervisor が管理する常駐プロセス
type Unit struct {
	Name    string
	Path    string
	Args    []string
	LogPath string
}

// node 内の unit を起動し、終了時にまとめて停止する
type Supervisor struct {
	units []Unit
	procs []*exec.Cmd
}

func NewSupervisor(units []Unit) *Supervisor {
	return &Supervisor{units: units}
}

// unit を起動し、いずれかが終了するまで待機する
func (supervisor *Supervisor) Run(ctx context.Context) error {
	for _, unit := range supervisor.units {
		process, err := startUnit(unit)
		if err != nil {
			supervisor.Stop()
			return err
		}
		supervisor.procs = append(supervisor.procs, process)
	}

	done := make(chan error, len(supervisor.procs))
	for _, process := range supervisor.procs {
		go func(process *exec.Cmd) { done <- process.Wait() }(process)
	}

	select {
	case <-ctx.Done():
		supervisor.Stop()
		supervisor.waitForExit(done, len(supervisor.procs))
		return nil
	case err := <-done:
		supervisor.Stop()
		supervisor.waitForExit(done, len(supervisor.procs)-1)
		if err != nil {
			return fmt.Errorf("supervised process exited: %w", err)
		}
		return nil
	}
}

// 管理中の process group に終了シグナルを送り、残ったプロセスを回収する
func (supervisor *Supervisor) Stop() {
	for _, process := range supervisor.procs {
		if process.Process == nil {
			continue
		}
		_ = syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
	}
}

func (supervisor *Supervisor) waitForExit(done <-chan error, count int) {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for count > 0 {
		select {
		case <-done:
			count--
		case <-deadline.C:
			for _, process := range supervisor.procs {
				if process.Process != nil {
					_ = process.Process.Kill()
				}
			}
			return
		}
	}
}

func startUnit(unit Unit) (*exec.Cmd, error) {
	logFile, err := os.OpenFile(unit.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s log: %w", unit.Name, err)
	}

	process := exec.Command(unit.Path, unit.Args...)
	process.Stdout = logFile
	process.Stderr = logFile
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := process.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start %s: %w", unit.Name, err)
	}
	logFile.Close()
	return process, nil
}
