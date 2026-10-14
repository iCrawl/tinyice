//go:build windows

package relay

import (
	"os/exec"
	"time"
)

func cancelCommandTree(cmd *exec.Cmd) { cmd.WaitDelay = 100 * time.Millisecond }
