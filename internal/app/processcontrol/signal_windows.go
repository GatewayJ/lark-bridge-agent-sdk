//go:build windows

package processcontrol

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const processQueryLimitedInformation = 0x1000

func terminate(pid int) error {
	if !Alive(pid) {
		return nil
	}
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T")
	output, err := cmd.CombinedOutput()
	if err == nil || !Alive(pid) {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("taskkill terminate failed: %s", detail)
}

func kill(pid int) error {
	if !Alive(pid) {
		return nil
	}
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	output, err := cmd.CombinedOutput()
	if err == nil || !Alive(pid) {
		return nil
	}
	process, findErr := os.FindProcess(pid)
	if findErr == nil {
		if killErr := process.Kill(); killErr == nil || !Alive(pid) {
			return nil
		}
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("taskkill force failed: %s", detail)
}

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err == nil {
		_ = syscall.CloseHandle(handle)
		return true
	}
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
