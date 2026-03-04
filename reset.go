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

	// signalSettleDelay is a short delay between DTR/RTS changes.
	// On Windows, SetDTR and SetRTS each perform a full DCB read-modify-write
	// via SetCommState which re-applies the entire port configuration.
	// Without a delay, rapid consecutive calls can cause signal glitches
	// where one signal briefly changes state when the other is modified.
	// USB CDC-ACM drivers (used by USB-JTAG/Serial) are especially sensitive
	// to this because signal changes traverse the USB stack.
	signalSettleDelay = 10 * time.Millisecond
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
	port.SetDTR(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle before changing RTS
	port.SetRTS(true)             //nolint:errcheck
	time.Sleep(delay)

	// IO0=LOW (request bootloader), EN=HIGH (release reset)
	// Set DTR first (GPIO0=LOW for bootloader mode), then release EN.
	// The order matters: on Windows, each SetXxx call rewrites the full
	// DCB via SetCommState, so we must ensure GPIO0 is LOW before EN
	// goes HIGH to guarantee bootloader entry.
	port.SetDTR(true)             //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle before changing RTS
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(tightResetDelay)

	// IO0=HIGH (release GPIO0)
	port.SetDTR(false) //nolint:errcheck
}

// tightReset performs a tighter reset timing variant.
// Some Linux serial drivers need DTR and RTS set simultaneously.
func tightReset(port serial.Port, delay time.Duration) {
	// Start from known state: both deasserted
	port.SetDTR(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay)

	// EN=LOW, IO0=LOW (hold in reset with bootloader select)
	port.SetDTR(true)             //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle
	port.SetRTS(true)             //nolint:errcheck
	time.Sleep(delay)

	// Release: IO0=LOW (bootloader), EN=HIGH (run)
	port.SetDTR(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(tightResetDelay)

	port.SetDTR(false) //nolint:errcheck
}

// usbJTAGSerialReset performs reset for USB-JTAG/Serial interfaces.
// Used on ESP32-C3, ESP32-S3, ESP32-C6, ESP32-H2 when using the
// built-in USB-JTAG/Serial peripheral.
func usbJTAGSerialReset(port serial.Port) {
	// Start from known idle state.
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let RTS settle
	port.SetDTR(false)            //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	// Set DTR to request bootloader mode on next reset.
	port.SetDTR(true)             //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	// Trigger reset: RTS HIGH → LOW with DTR LOW.
	port.SetRTS(true)             //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let RTS settle
	port.SetDTR(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let DTR settle
	port.SetRTS(true)             //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	// Return to idle: both deasserted.
	port.SetRTS(false)            //nolint:errcheck
	time.Sleep(signalSettleDelay) // Let RTS settle
	port.SetDTR(false)            //nolint:errcheck
}

// hardReset performs a hardware reset (chip restarts and runs user code).
//
// For USB-JTAG/Serial connections (ESP32-S3, ESP32-C3, etc.), the sequence
// uses longer delays to allow USB device re-enumeration, and starts from a
// known-clean signal state. This is critical on Windows where USB CDC-ACM
// drivers need additional time and may latch DTR/RTS state.
func hardReset(port serial.Port, usesUSB bool) {
	if usesUSB {
		// USB-JTAG/Serial: use the same RTS toggle to reset the chip,
		// but ensure DTR is LOW (normal boot, not bootloader) and add
		// longer delays for USB device re-enumeration.
		// Start from known idle state.
		port.SetRTS(false)            //nolint:errcheck
		time.Sleep(signalSettleDelay) // Let RTS settle
		port.SetDTR(false)            //nolint:errcheck
		time.Sleep(100 * time.Millisecond)

		// Assert reset
		port.SetRTS(true) //nolint:errcheck
		time.Sleep(100 * time.Millisecond)

		// Release reset — chip boots into normal mode (DTR=false → GPIO0=HIGH)
		port.SetRTS(false)                 //nolint:errcheck
		time.Sleep(200 * time.Millisecond) // Allow USB re-enumeration

		// Ensure clean final state
		port.SetDTR(false) //nolint:errcheck
		port.SetRTS(false) //nolint:errcheck
	} else {
		// Classic UART bridge: simple RTS toggle.
		// Ensure DTR is deasserted so GPIO0=HIGH (normal boot).
		port.SetDTR(false)            //nolint:errcheck
		time.Sleep(signalSettleDelay) // Let DTR settle
		port.SetRTS(true)             //nolint:errcheck
		time.Sleep(100 * time.Millisecond)
		port.SetRTS(false) //nolint:errcheck

		// Ensure clean final state so Windows doesn't latch bad values
		// when the port is closed.
		time.Sleep(signalSettleDelay)
		port.SetDTR(false) //nolint:errcheck
		port.SetRTS(false) //nolint:errcheck
	}
}
