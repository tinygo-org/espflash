package espflasher

import "fmt"

// Error types for ESP flash operations.

// CommandError is returned when the device returns a non-zero status.
type CommandError struct {
	OpCode  byte
	Status  byte
	ErrCode byte
}

func (e *CommandError) Error() string {
	// The v1.1.0+ esp-flasher-stub uses 16-bit big-endian response codes
	// where the high byte (Status) encodes the error category and the low
	// byte (ErrCode) is always 0x00. Earlier ROM bootloaders use Status=1
	// with the low byte as the detailed error code.
	desc := "unknown error"
	switch {
	// ROM bootloader error codes (Status == 0x01, error in ErrCode)
	case e.Status == 0x01:
		switch e.ErrCode {
		case 0x05:
			desc = "received message is invalid"
		case 0x06:
			desc = "failed to act on received message"
		case 0x07:
			desc = "invalid CRC in message"
		case 0x08:
			desc = "flash write error"
		case 0x09:
			desc = "flash read error"
		case 0x0A:
			desc = "flash read length error"
		case 0x0B:
			desc = "deflate error"
		}
	// Stub response codes (16-bit big-endian, ErrCode == 0x00)
	case e.Status == 0xC0 && e.ErrCode == 0x00:
		desc = "bad data length"
	case e.Status == 0xC1 && e.ErrCode == 0x00:
		desc = "bad data checksum"
	case e.Status == 0xC2 && e.ErrCode == 0x00:
		desc = "bad block size"
	case e.Status == 0xC3 && e.ErrCode == 0x00:
		desc = "invalid command"
	case e.Status == 0xC4 && e.ErrCode == 0x00:
		desc = "SPI operation failed"
	case e.Status == 0xC5 && e.ErrCode == 0x00:
		desc = "SPI unlock failed"
	case e.Status == 0xC6 && e.ErrCode == 0x00:
		desc = "not in flash mode"
	case e.Status == 0xC7 && e.ErrCode == 0x00:
		desc = "inflate error"
	case e.Status == 0xC8 && e.ErrCode == 0x00:
		desc = "not enough data"
	case e.Status == 0xC9 && e.ErrCode == 0x00:
		desc = "too much data"
	case e.Status == 0xFF && e.ErrCode == 0x00:
		desc = "command not implemented"
	}
	return fmt.Sprintf("command 0x%02X failed: status=0x%02X error=0x%02X (%s)",
		e.OpCode, e.Status, e.ErrCode, desc)
}

// IsRetryable returns true if the error indicates a transient serial data
// loss (bad length or checksum) rather than a persistent device-side failure.
// These errors mean the SLIP frame was corrupted during UART transmission
// but the stub is in a clean state and ready for a resend.
func (e *CommandError) IsRetryable() bool {
	return (e.Status == 0xC0 || e.Status == 0xC1) && e.ErrCode == 0x00
}

// TimeoutError is returned when a response is not received within the timeout.
type TimeoutError struct {
	Op string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout waiting for response to %s", e.Op)
}

// SyncError is returned when the device cannot be synced.
type SyncError struct {
	Attempts int
}

func (e *SyncError) Error() string {
	return fmt.Sprintf("failed to sync with ESP bootloader after %d attempts", e.Attempts)
}

// ChipDetectError is returned when chip auto-detection fails.
type ChipDetectError struct {
	MagicValue uint32
}

func (e *ChipDetectError) Error() string {
	return fmt.Sprintf("failed to detect chip type (magic value: 0x%08X)", e.MagicValue)
}

// UnsupportedCommandError is returned for commands not supported by the current ROM/stub.
type UnsupportedCommandError struct {
	Command string
}

func (e *UnsupportedCommandError) Error() string {
	return fmt.Sprintf("command %s is not supported on this chip/loader", e.Command)
}
