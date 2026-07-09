package main

import (
	"context"
	"time"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/processcontrol"
)

const (
	defaultProcessStopTimeout = 2 * time.Second
	processStopPollInterval   = 100 * time.Millisecond
)

type stopProcessResult string

const (
	stopProcessTerminated stopProcessResult = "terminated"
	stopProcessKilled     stopProcessResult = "killed"
)

func stopProcessEntry(ctx context.Context, pid int, timeout time.Duration) (stopProcessResult, bool, error) {
	result, stillAlive, err := processcontrol.Stop(ctx, pid, timeout)
	return stopProcessResult(result), stillAlive, err
}

func waitProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	return processcontrol.WaitExit(ctx, pid, timeout)
}
