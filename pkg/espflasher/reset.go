package espflasher

import (
	"fmt"
	"time"

	"go.bug.st/serial"
)

// ResetMode defines how the ESP chip should be reset.
type ResetMode int

const (
	// ResetDefault uses the classic DTR/RTS reset sequence to enter bootloader.
	ResetDefault ResetMode = iota

	// ResetNoReset does not perform any hardware reset.
	// The chip must already be in bootloader mode.
	ResetNoReset

	// ResetUSBJTAG uses the USB-JTAG/Serial reset sequence (ESP32-S3, ESP32-C3, etc.).
	ResetUSBJTAG

	// ResetAuto tries multiple reset strategies in sequence.
	// First attempts DTR/RTS classic reset, then USB-JTAG, then no-signal.
	// Useful when the interface type is unknown.
	ResetAuto
)

const (
	// defaultResetDelay is the standard delay during reset sequences.
	// Matches esptool.py's DEFAULT_RESET_DELAY.
	defaultResetDelay = 50 * time.Millisecond

	// extraResetDelay is used for longer-duration reset cycles on some devices.
	extraResetDelay = 550 * time.Millisecond
)

// setDTRandRTS sets DTR and RTS simultaneously.
// On Unix systems (darwin, linux), uses atomic TIOCMSET ioctl to set both lines
// in a single operation. This is important for CH340 boards that require precise
// timing of the DTR/RTS transition.
// On other systems, falls back to separate SetDTR and SetRTS calls.
func setDTRandRTS(port serial.Port, dtr, rts bool) error {
	// On Unix systems, try to use atomic TIOCMSET for precise timing
	err := setDTRandRTSAtomic(port, dtr, rts)
	if err == nil {
		// Atomic operation succeeded
		return nil
	}

	// Fallback: use separate calls (always used on non-Unix, used on Unix if atomic fails)
	if err := port.SetDTR(dtr); err != nil {
		return err
	}
	return port.SetRTS(rts)
}

// classicReset performs the classic DTR/RTS bootloader entry sequence.
//
// This is the standard reset sequence used by most USB-UART bridges:
//
//  1. Assert DTR (IO0 LOW) and deassert RTS (EN HIGH)
//  2. Assert RTS (EN LOW) to hold chip in reset
//  3. Deassert DTR (IO0 HIGH for normal boot)
//  4. Wait briefly
//  5. Deassert RTS (EN HIGH) to release reset → chip boots into bootloader
//     because IO0 was LOW at the moment EN went HIGH
//  6. Deassert DTR (IO0 back to HIGH)
//
// On typical USB-UART bridges (e.g., CH340, CP2102):
//   - DTR controls GPIO0: DTR=true → GPIO0=LOW  (bootloader mode)
//   - RTS controls EN:    RTS=true → EN=LOW     (chip in reset)
func classicReset(port serial.Port, delay time.Duration) {
	// IO0=HIGH, EN=LOW (hold in reset)
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(true)  //nolint:errcheck
	time.Sleep(delay)

	// IO0=LOW (request bootloader), EN=HIGH (release reset)
	port.SetDTR(true)  //nolint:errcheck
	port.SetRTS(false) //nolint:errcheck
	time.Sleep(defaultResetDelay)

	// IO0=HIGH (release GPIO0)
	port.SetDTR(false) //nolint:errcheck
}

// unixTightReset performs the esptool.py UnixTightReset sequence.
// This uses atomic DTR/RTS transitions for precise timing on Unix systems.
// It matches esptool.py's reset sequence to better support CH340 boards on macOS/Linux.
//
// Sequence (using atomic setDTRandRTS where possible):
//  1. setDTRandRTS(false, false) - IO0=HIGH, EN=HIGH (idle)
//  2. setDTRandRTS(true, true)   - IO0=LOW, EN=LOW
//  3. setDTRandRTS(false, true)  - IO0=HIGH, EN=LOW (chip held in reset)
//  4. Sleep 100ms
//  5. setDTRandRTS(true, false)  - IO0=LOW, EN=HIGH (bootloader mode)
//  6. Sleep delay ms
//  7. setDTRandRTS(false, false) - IO0=HIGH, EN=HIGH
//  8. SetDTR(false) - ensure IO0 is released
func unixTightReset(port serial.Port, delay time.Duration) {
	setDTRandRTS(port, false, false) //nolint:errcheck
	setDTRandRTS(port, true, true)   //nolint:errcheck
	setDTRandRTS(port, false, true)  //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	setDTRandRTS(port, true, false) //nolint:errcheck
	time.Sleep(delay)

	setDTRandRTS(port, false, false) //nolint:errcheck
	port.SetDTR(false)               //nolint:errcheck
}

// tightReset performs a tighter reset timing variant.
// Some Linux serial drivers need DTR and RTS set simultaneously.
func tightReset(port serial.Port, delay time.Duration) {
	// Both LOW (IO0=LOW, EN=LOW)
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(false) //nolint:errcheck

	// EN=LOW, IO0=LOW
	port.SetDTR(true) //nolint:errcheck
	port.SetRTS(true) //nolint:errcheck
	time.Sleep(delay)

	// Release: IO0=LOW (bootloader), EN=HIGH (run)
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(false) //nolint:errcheck
	time.Sleep(defaultResetDelay)

	port.SetDTR(false) //nolint:errcheck
}

// usbJTAGSerialReset performs reset for USB-JTAG/Serial interfaces.
// Used on ESP32-C3, ESP32-S3, ESP32-C5, ESP32-C6, ESP32-H2 when using the
// built-in USB-JTAG/Serial peripheral.
//
// The sequence matches esptool's USBJTAGSerialReset: assert DTR (IO0=LOW
// for bootloader), then toggle RTS through (1,1) to trigger reset, then
// release. After SetRTS(true) the USB device may disconnect; subsequent
// ioctls may block on Linux's cdc_acm driver, so callers should run this
// in a goroutine with a timeout.
func usbJTAGSerialReset(port serial.Port) {
	port.SetRTS(false) //nolint:errcheck
	port.SetDTR(false) //nolint:errcheck // Idle
	time.Sleep(100 * time.Millisecond)

	port.SetDTR(true)  //nolint:errcheck // IO0=LOW (bootloader mode)
	port.SetRTS(false) //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	// Trigger reset. Go through (1,1) instead of (0,0).
	// USB device may disconnect here.
	port.SetRTS(true)  //nolint:errcheck // EN=LOW (reset)
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(true)  //nolint:errcheck // Propagate DTR on RTS (Windows)
	time.Sleep(100 * time.Millisecond)
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(false) //nolint:errcheck // EN=HIGH (chip out of reset)
}

// hardReset performs a hardware reset (chip restarts and runs user code).
func hardReset(port serial.Port, usesUSB bool) {
	if usesUSB {
		// On USB-JTAG/Serial, the peripheral latches DTR (GPIO0 state)
		// at reset time. Ensure DTR=false so GPIO0=HIGH → normal boot,
		// not bootloader mode.
		port.SetDTR(false) //nolint:errcheck
	}
	port.SetRTS(true) //nolint:errcheck // EN=LOW (chip in reset)
	if usesUSB {
		time.Sleep(200 * time.Millisecond)
		port.SetRTS(false) //nolint:errcheck
		time.Sleep(200 * time.Millisecond)
	} else {
		time.Sleep(100 * time.Millisecond)
		// Release DTR before exiting reset. Otherwise a leftover DTR=true
		// from a prior operation holds IO0 LOW at reset exit and the chip
		// boots into the download-mode bootloader instead of the
		// application. Matches esptool.py HardReset.
		port.SetDTR(false) //nolint:errcheck
		port.SetRTS(false) //nolint:errcheck
	}
}

// String returns the string representation of the ResetMode.
func (r ResetMode) String() string {
	switch r {
	case ResetDefault:
		return "default"
	case ResetNoReset:
		return "no-reset"
	case ResetUSBJTAG:
		return "usb-jtag"
	case ResetAuto:
		return "auto"
	default:
		return fmt.Sprintf("unknown(%d)", int(r))
	}
}
