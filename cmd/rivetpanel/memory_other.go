//go:build !linux

package main

// disableTransparentHugePages is Linux-only.
func disableTransparentHugePages() bool { return false }
