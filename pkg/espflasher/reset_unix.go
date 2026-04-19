//go:build darwin || linux

package espflasher

import (
	"errors"
	"reflect"

	"golang.org/x/sys/unix"
)

var errNoHandle = errors.New("port does not have accessible file descriptor")

// setDTRandRTSAtomic performs an atomic TIOCMSET ioctl on Unix systems.
// It attempts to use the underlying file descriptor to set DTR and RTS
// simultaneously, which is important for precise timing on CH340 boards.
// Returns errNoHandle if the port doesn't have an accessible fd (signals to try separate calls).
func setDTRandRTSAtomic(port interface{}, dtr, rts bool) error {
	// Try to get the underlying file descriptor via type assertion.
	// go.bug.st/serial's unixPort has a handle field (int), but it's unexported.
	// We'll use reflect to access it.
	v := reflect.ValueOf(port)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// Look for a field named "handle" (fd)
	handleField := v.FieldByName("handle")
	if !handleField.IsValid() || handleField.Kind() != reflect.Int {
		// Field doesn't exist or wrong type; caller will fall back to separate calls
		return errNoHandle
	}

	fd := int(handleField.Int())

	// Build the TIOCMSET bitmask
	var status int
	if dtr {
		status |= unix.TIOCM_DTR
	}
	if rts {
		status |= unix.TIOCM_RTS
	}

	// Perform the atomic ioctl
	return unix.IoctlSetPointerInt(fd, unix.TIOCMSET, status)
}
