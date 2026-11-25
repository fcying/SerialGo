//go:build linux

package internal

import "syscall"

const outputNonblock = syscall.O_NONBLOCK
