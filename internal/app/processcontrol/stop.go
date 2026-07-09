package processcontrol

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultStopTimeout = 2 * time.Second
	stopPollInterval   = 100 * time.Millisecond
)

type StopResult string

const (
	StopResultTerminated StopResult = "terminated"
	StopResultKilled     StopResult = "killed"
)

func Stop(ctx context.Context, pid int, timeout time.Duration) (StopResult, bool, error) {
	if pid <= 0 {
		return "", false, fmt.Errorf("process pid is missing")
	}
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	if err := terminate(pid); err != nil {
		return "", true, err
	}
	if WaitExit(ctx, pid, timeout) {
		return StopResultTerminated, false, nil
	}
	if err := kill(pid); err != nil {
		return "", true, err
	}
	if WaitExit(ctx, pid, timeout) {
		return StopResultKilled, false, nil
	}
	return "", true, fmt.Errorf("process %d did not exit after force kill", pid)
}

func WaitExit(ctx context.Context, pid int, timeout time.Duration) bool {
	if !Alive(pid) {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return !Alive(pid)
		case <-ticker.C:
			if !Alive(pid) {
				return true
			}
		}
	}
}
