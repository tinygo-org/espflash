package espflash

import (
	"strings"
	"testing"
)

func TestCommandError(t *testing.T) {
	tests := []struct {
		name     string
		errCode  byte
		contains string
	}{
		{"invalid message", 0x05, "received message is invalid"},
		{"failed to act", 0x06, "failed to act on received message"},
		{"invalid CRC", 0x07, "invalid CRC in message"},
		{"flash write", 0x08, "flash write error"},
		{"flash read", 0x09, "flash read error"},
		{"flash read length", 0x0A, "flash read length error"},
		{"deflate error", 0x0B, "deflate error"},
		{"unknown error", 0xFF, "unknown error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &CommandError{
				OpCode:  0x02,
				Status:  0x01,
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
