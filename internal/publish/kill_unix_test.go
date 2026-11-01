//go:build unix

package publish

import (
	"os"
	"syscall"
)

const killedExitCode = -1

func killSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); select {} }
