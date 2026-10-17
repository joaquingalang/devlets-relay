//go:build !windows

package main

import "os"

func isMsysPty(*os.File) bool { return false }
