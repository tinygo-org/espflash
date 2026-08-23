//go:build !darwin || cgo

package espflasher

import (
	"strings"

	"go.bug.st/serial/enumerator"
)

// portVIDPID looks up a port's USB VID/PID by name. A variable so tests can
// stub enumeration. Excluded on macOS without cgo, where enumerator needs
// IOKit; see usbjtag_nocgo.go.
var portVIDPID = enumeratePortVIDPID

// enumeratePortVIDPID returns the upper-case VID/PID of the named port. ok
// is false if it isn't found, isn't USB, or reports no VID/PID.
func enumeratePortVIDPID(name string) (vid, pid string, ok bool) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", "", false
	}
	for _, p := range ports {
		if !p.IsUSB || !samePortName(p.Name, name) {
			continue
		}
		if p.VID == "" || p.PID == "" {
			return "", "", false
		}
		return strings.ToUpper(p.VID), strings.ToUpper(p.PID), true
	}
	return "", "", false
}
