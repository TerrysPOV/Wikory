//go:build unix

package store

import (
	"os"
	"syscall"
)

const killedExitCode = -1 // a process killed by a signal reports ExitCode() == -1

func killSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); select {} }
