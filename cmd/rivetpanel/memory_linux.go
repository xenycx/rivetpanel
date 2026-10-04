package main

import (
	"os"
	"strings"
	"syscall"
)

// prSetTHPDisable is PR_SET_THP_DISABLE from <linux/prctl.h>.
const prSetTHPDisable = 41

// disableTransparentHugePages opts this process out of transparent huge pages.
//
// On hosts where THP is "always" (several distributions' default), khugepaged
// folds the Go heap into 2 MiB pages and refills the parts the runtime has
// returned to the kernel, so a panel with 7 MiB of heap in use kept about
// 14 MiB resident. The panel's heap is small and gains nothing from huge
// pages. The setting covers only this process (the panel starts no child
// processes); containers are unaffected.
//
// An operator who sets disablethp in GODEBUG decides instead: disablethp=1
// lets the Go runtime do the same for the heap alone, disablethp=0 keeps huge
// pages.
func disableTransparentHugePages() bool {
	if godebugSets(os.Getenv("GODEBUG"), "disablethp") {
		return false
	}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetTHPDisable, 1, 0, 0, 0, 0)
	return errno == 0
}

// godebugSets reports whether the GODEBUG value names key.
func godebugSets(godebug, key string) bool {
	for _, kv := range strings.Split(godebug, ",") {
		if k, _, _ := strings.Cut(strings.TrimSpace(kv), "="); k == key {
			return true
		}
	}
	return false
}
