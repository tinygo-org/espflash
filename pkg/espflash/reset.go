package espflash

import (
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
)

const (
	// defaultResetDelay is the standard delay during reset sequences.
	defaultResetDelay = 100 * time.Millisecond

	// tightResetDelay is a shorter delay for Unix systems.
	tightResetDelay = 50 * time.Millisecond
)

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
	time.Sleep(tightResetDelay)

	// IO0=HIGH (release GPIO0)
	port.SetDTR(false) //nolint:errcheck
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
	time.Sleep(tightResetDelay)

	port.SetDTR(false) //nolint:errcheck
}

// usbJTAGSerialReset performs reset for USB-JTAG/Serial interfaces.
// Used on ESP32-C3, ESP32-S3, ESP32-C6, ESP32-H2 when using the
// built-in USB-JTAG/Serial peripheral.
func usbJTAGSerialReset(port serial.Port) {
	port.SetRTS(false) //nolint:errcheck
	port.SetDTR(false) //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	port.SetDTR(true)  //nolint:errcheck
	port.SetRTS(false) //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	port.SetRTS(true)  //nolint:errcheck
	port.SetDTR(false) //nolint:errcheck
	port.SetRTS(true)  //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	port.SetRTS(false) //nolint:errcheck
	port.SetDTR(false) //nolint:errcheck
}

// hardReset performs a hardware reset (chip restarts and runs user code).
func hardReset(port serial.Port, usesUSB bool) {
	port.SetRTS(true) //nolint:errcheck
	if usesUSB {
		time.Sleep(200 * time.Millisecond)
		port.SetRTS(false) //nolint:errcheck
		time.Sleep(200 * time.Millisecond)
	} else {
		time.Sleep(100 * time.Millisecond)
		port.SetRTS(false) //nolint:errcheck
	}
}
