//go:build !windows

package espflash

import "go.bug.st/serial"

// setDTR drives the DTR signal.
// On non-Windows platforms, the serial library uses ioctl (TIOCMSET)
// which atomically sets signal lines without the coupling issues
// present in the Windows DCB-based approach.
func setDTR(port serial.Port, dtr bool) {
	port.SetDTR(dtr) //nolint:errcheck
}

// setRTS drives the RTS signal.
// On non-Windows platforms, the serial library uses ioctl (TIOCMSET)
// which atomically sets signal lines without the coupling issues
// present in the Windows DCB-based approach.
func setRTS(port serial.Port, rts bool) {
	port.SetRTS(rts) //nolint:errcheck
}
