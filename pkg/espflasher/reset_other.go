//go:build !windows

package espflasher

import "go.bug.st/serial"

// hardResetUSB performs a hardware reset for USB-JTAG/Serial devices.
// On non-Windows platforms, this delegates to the standard hardReset
// which uses SetRTS/SetDTR through the serial library.
func hardResetUSB(port serial.Port) error {
	return hardReset(port, true)
}
