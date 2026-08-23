//go:build windows

package espflasher

import (
	"time"

	"go.bug.st/serial"
)

// hardResetUSB performs a hardware reset for USB-JTAG/Serial devices on Windows.
// On Windows, a single hardReset is not sufficient for USB CDC devices.
// Performing two resets (first non-USB, then USB timing) reliably triggers
// the chip to restart.
func hardResetUSB(port serial.Port) error {
	err := hardReset(port, false)
	time.Sleep(defaultResetDelay)
	if err2 := hardReset(port, true); err == nil {
		err = err2
	}
	return err
}
