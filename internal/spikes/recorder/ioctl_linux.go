//go:build spike && linux

package main

import "golang.org/x/sys/unix"

const ioctlGetTermios = unix.TCGETS
