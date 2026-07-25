package espflasher

import (
	"strings"
	"testing"
)

func TestCommandError(t *testing.T) {
	tests := []struct {
		name     string
		status   byte
		errCode  byte
		contains string
	}{
		// ROM bootloader error codes
		{"invalid message", 0x01, 0x05, "received message is invalid"},
		{"failed to act", 0x01, 0x06, "failed to act on received message"},
		{"invalid CRC", 0x01, 0x07, "invalid CRC in message"},
		{"flash write", 0x01, 0x08, "flash write error"},
		{"flash read", 0x01, 0x09, "flash read error"},
		{"flash read length", 0x01, 0x0A, "flash read length error"},
		{"deflate error", 0x01, 0x0B, "deflate error"},
		{"unknown ROM error", 0x01, 0xFF, "unknown error"},
		// Stub response codes (v1.1.0+)
		{"bad data length", 0xC0, 0x00, "bad data length"},
		{"bad data checksum", 0xC1, 0x00, "bad data checksum"},
		{"bad block size", 0xC2, 0x00, "bad block size"},
		{"invalid command", 0xC3, 0x00, "invalid command"},
		{"SPI op failed", 0xC4, 0x00, "SPI operation failed"},
		{"SPI unlock failed", 0xC5, 0x00, "SPI unlock failed"},
		{"not in flash mode", 0xC6, 0x00, "not in flash mode"},
		{"inflate error", 0xC7, 0x00, "inflate error"},
		{"not enough data", 0xC8, 0x00, "not enough data"},
		{"too much data", 0xC9, 0x00, "too much data"},
		{"cmd not implemented", 0xFF, 0x00, "command not implemented"},
		{"unknown stub error", 0xCF, 0x00, "unknown error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &CommandError{
				OpCode:  0x02,
				Status:  tt.status,
				ErrCode: tt.errCode,
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.contains) {
				t.Errorf("CommandError.Error() = %q, want to contain %q", msg, tt.contains)
			}
			// Should include the opcode hex
			if !strings.Contains(msg, "0x02") {
				t.Errorf("CommandError.Error() = %q, should contain opcode 0x02", msg)
			}
		})
	}
}

func TestCommandErrorFormat(t *testing.T) {
	err := &CommandError{OpCode: 0x10, Status: 0x01, ErrCode: 0x05}
	msg := err.Error()
	// Should have format: "command 0x10 failed: status=0x01 error=0x05 (received message is invalid)"
	if !strings.HasPrefix(msg, "command 0x10 failed:") {
		t.Errorf("unexpected format: %q", msg)
	}
	if !strings.Contains(msg, "status=0x01") {
		t.Errorf("missing status in: %q", msg)
	}
	if !strings.Contains(msg, "error=0x05") {
		t.Errorf("missing error code in: %q", msg)
	}
}

func TestCommandErrorIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		status    byte
		errCode   byte
		retryable bool
	}{
		{"bad data length", 0xC0, 0x00, true},
		{"bad data checksum", 0xC1, 0x00, true},
		{"SPI op failed", 0xC4, 0x00, false},
		{"inflate error", 0xC7, 0x00, false},
		{"ROM error", 0x01, 0x05, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &CommandError{Status: tt.status, ErrCode: tt.errCode}
			if err.IsRetryable() != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", err.IsRetryable(), tt.retryable)
			}
		})
	}
}

func TestTimeoutError(t *testing.T) {
	err := &TimeoutError{Op: "sync"}
	msg := err.Error()
	if !strings.Contains(msg, "timeout") {
		t.Errorf("TimeoutError.Error() = %q, should contain 'timeout'", msg)
	}
	if !strings.Contains(msg, "sync") {
		t.Errorf("TimeoutError.Error() = %q, should contain op name", msg)
	}
}

func TestSyncError(t *testing.T) {
	err := &SyncError{Attempts: 7}
	msg := err.Error()
	if !strings.Contains(msg, "sync") {
		t.Errorf("SyncError.Error() = %q, should contain 'sync'", msg)
	}
	if !strings.Contains(msg, "7") {
		t.Errorf("SyncError.Error() = %q, should contain attempt count", msg)
	}
}

func TestChipDetectError(t *testing.T) {
	err := &ChipDetectError{MagicValue: 0xDEADBEEF}
	msg := err.Error()
	if !strings.Contains(msg, "detect") {
		t.Errorf("ChipDetectError.Error() = %q, should contain 'detect'", msg)
	}
	if !strings.Contains(msg, "DEADBEEF") {
		t.Errorf("ChipDetectError.Error() = %q, should contain magic value hex", msg)
	}
}

func TestUnsupportedCommandError(t *testing.T) {
	err := &UnsupportedCommandError{Command: "erase flash (requires stub)"}
	msg := err.Error()
	if !strings.Contains(msg, "not supported") {
		t.Errorf("UnsupportedCommandError.Error() = %q, should contain 'not supported'", msg)
	}
	if !strings.Contains(msg, "erase flash") {
		t.Errorf("UnsupportedCommandError.Error() = %q, should contain command name", msg)
	}
}

func TestErrorsImplementErrorInterface(t *testing.T) {
	// Verify all error types implement the error interface.
	var _ error = &CommandError{}
	var _ error = &TimeoutError{}
	var _ error = &SyncError{}
	var _ error = &ChipDetectError{}
	var _ error = &UnsupportedCommandError{}
}
