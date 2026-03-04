package espflash

import "fmt"

// Error types for ESP flash operations.

// CommandError is returned when the ROM bootloader returns a non-zero status.
type CommandError struct {
	OpCode  byte
	Status  byte
	ErrCode byte
}

func (e *CommandError) Error() string {
	desc := "unknown error"
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
	return fmt.Sprintf("command 0x%02X failed: status=0x%02X error=0x%02X (%s)",
		e.OpCode, e.Status, e.ErrCode, desc)
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
