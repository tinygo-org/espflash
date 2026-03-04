//go:build windows

package espflash

import (
	"fmt"
	"reflect"
	"syscall"
	"unsafe"

	"go.bug.st/serial"
	"golang.org/x/sys/windows"
)

// Windows EscapeCommFunction constants for direct signal control.
const (
	escSETRTS = 3
	escCLRRTS = 4
	escSETDTR = 5
	escCLRDTR = 6
)

var procEscapeCommFunction = windows.NewLazySystemDLL("kernel32.dll").NewProc("EscapeCommFunction")

// winEscapeCommFunction calls the Win32 EscapeCommFunction API directly.
// This drives DTR/RTS signals immediately without rewriting the DCB,
// avoiding the signal coupling bugs caused by SetCommState.
func winEscapeCommFunction(handle syscall.Handle, function uint32) error {
	r1, _, err := procEscapeCommFunction.Call(uintptr(handle), uintptr(function))
	if r1 == 0 {
		return fmt.Errorf("EscapeCommFunction(%d): %w", function, err)
	}
	return nil
}

// getPortHandle extracts the underlying syscall.Handle from a go.bug.st/serial
// Port on Windows.
//
// The serial library's windowsPort struct has layout:
//
//	type windowsPort struct {
//	    mu     sync.Mutex     // 8 bytes
//	    handle syscall.Handle // at offset 8
//	}
//
// We use reflect to access the unexported "handle" field. This is necessary
// because the serial library's SetDTR/SetRTS on Windows use the DCB-based
// approach (GetCommState + modify + SetCommState), which reinitializes all
// hardware on every call and causes signal coupling where changing one signal
// glitches the other. EscapeCommFunction avoids this entirely.
func getPortHandle(port serial.Port) (syscall.Handle, bool) {
	v := reflect.ValueOf(port)
	if v.Kind() != reflect.Ptr {
		return 0, false
	}
	v = v.Elem()
	hf := v.FieldByName("handle")
	if !hf.IsValid() || !hf.CanAddr() {
		return 0, false
	}
	// Use unsafe to read the unexported field value.
	handle := *(*syscall.Handle)(unsafe.Pointer(hf.UnsafeAddr()))
	if handle == 0 {
		return 0, false
	}
	return handle, true
}

// setDTR drives the DTR signal using EscapeCommFunction on Windows.
// Falls back to the library's SetDTR if the handle cannot be extracted.
func setDTR(port serial.Port, dtr bool) {
	handle, ok := getPortHandle(port)
	if !ok {
		port.SetDTR(dtr) //nolint:errcheck
		return
	}
	if dtr {
		winEscapeCommFunction(handle, escSETDTR) //nolint:errcheck
	} else {
		winEscapeCommFunction(handle, escCLRDTR) //nolint:errcheck
	}
}

// setRTS drives the RTS signal using EscapeCommFunction on Windows.
// Falls back to the library's SetRTS if the handle cannot be extracted.
func setRTS(port serial.Port, rts bool) {
	handle, ok := getPortHandle(port)
	if !ok {
		port.SetRTS(rts) //nolint:errcheck
		return
	}
	if rts {
		winEscapeCommFunction(handle, escSETRTS) //nolint:errcheck
	} else {
		winEscapeCommFunction(handle, escCLRRTS) //nolint:errcheck
	}
}
