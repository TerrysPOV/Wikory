//go:build spike && darwin

package main

import "golang.org/x/sys/unix"

const ioctlGetTermios = unix.TIOCGETA
