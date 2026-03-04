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

	// tightResetDelay is a shorter delay used in some reset variants.
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
//
// DTR/RTS are driven via the platform-specific setDTR/setRTS helpers.
// On Windows these use EscapeCommFunction for atomic signal control;
// on Unix they use ioctl (TIOCMSET) via the serial library.
func classicReset(port serial.Port, delay time.Duration) {
	// IO0=HIGH, EN=LOW (hold in reset)
	setDTR(port, false)
	setRTS(port, true)
	time.Sleep(delay)

	// IO0=LOW (request bootloader), EN=HIGH (release reset)
	setDTR(port, true)
	setRTS(port, false)
	time.Sleep(tightResetDelay)

	// IO0=HIGH (release GPIO0)
	setDTR(port, false)
}

// tightReset performs a tighter reset timing variant.
// Some Linux serial drivers need DTR and RTS set simultaneously.
func tightReset(port serial.Port, delay time.Duration) {
	// Start from known state: both deasserted
	setDTR(port, false)
	setRTS(port, false)

	// EN=LOW, IO0=LOW (hold in reset with bootloader select)
	setDTR(port, true)
	setRTS(port, true)
	time.Sleep(delay)

	// Release: IO0=LOW (bootloader), EN=HIGH (run)
	setDTR(port, false)
	setRTS(port, false)
	time.Sleep(tightResetDelay)

	setDTR(port, false)
}

// usbJTAGSerialReset performs reset for USB-JTAG/Serial interfaces.
// Used on ESP32-C3, ESP32-S3, ESP32-C6, ESP32-H2 when using the
// built-in USB-JTAG/Serial peripheral.
func usbJTAGSerialReset(port serial.Port) {
	// Start from known idle state.
	setRTS(port, false)
	setDTR(port, false)
	time.Sleep(100 * time.Millisecond)

	// Set DTR to request bootloader mode on next reset.
	setDTR(port, true)
	setRTS(port, false)
	time.Sleep(100 * time.Millisecond)

	// Trigger reset: RTS HIGH → LOW with DTR LOW.
	setRTS(port, true)
	setDTR(port, false)
	setRTS(port, true)
	time.Sleep(100 * time.Millisecond)

	// Return to idle: both deasserted.
	setRTS(port, false)
	setDTR(port, false)
}

// hardReset performs a hardware reset (chip restarts and runs user code).
//
// For USB-JTAG/Serial connections (ESP32-S3, ESP32-C3, etc.), the sequence
// uses longer delays to allow USB device re-enumeration.
func hardReset(port serial.Port, usesUSB bool) {
	if usesUSB {
		// USB-JTAG/Serial: ensure DTR is LOW (normal boot, not bootloader)
		// and use longer delays for USB device re-enumeration.
		setRTS(port, false)
		setDTR(port, false)
		time.Sleep(100 * time.Millisecond)

		// Assert reset
		setRTS(port, true)
		time.Sleep(100 * time.Millisecond)

		// Release reset — chip boots into normal mode (DTR=false → GPIO0=HIGH)
		setRTS(port, false)
		time.Sleep(200 * time.Millisecond) // Allow USB re-enumeration

		// Ensure clean final state
		setDTR(port, false)
		setRTS(port, false)
	} else {
		// Classic UART bridge: simple RTS toggle.
		// Ensure DTR is deasserted so GPIO0=HIGH (normal boot).
		setDTR(port, false)
		setRTS(port, true)
		time.Sleep(100 * time.Millisecond)
		setRTS(port, false)

		// Ensure clean final state
		setDTR(port, false)
		setRTS(port, false)
	}
}
