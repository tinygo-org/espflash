package espflasher

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.bug.st/serial"
)

func TestNewUsesSerialOpener(t *testing.T) {
	var openCount atomic.Int32
	mock := &mockPort{
		readFunc: func(p []byte) (int, error) {
			// Return sync response to allow connect() to proceed
			if len(p) >= 8 {
				// SLIP-encoded sync response (simplified)
				return 0, nil
			}
			return 0, nil
		},
	}

	opts := &FlasherOptions{
		BaudRate:        115200,
		FlashBaudRate:   460800,
		ChipType:        ChipAuto,
		ResetMode:       ResetNoReset,
		ConnectAttempts: 1,
		Compress:        true,
		SerialOpener: func(name string, mode *serial.Mode) (serial.Port, error) {
			openCount.Add(1)
			return mock, nil
		},
	}

	f, _ := New("fake", opts)
	// New may fail at connect step, but that's fine—we just verify the opener was called.
	if f != nil {
		defer f.Close() //nolint:errcheck
	}

	// Verify opener was called at least once (during New).
	if openCount.Load() == 0 {
		t.Errorf("SerialOpener was not called during New")
	}
}

func TestReopenPortUsesSerialOpener(t *testing.T) {
	var reopenCount atomic.Int32

	// Create initial mock
	initialMock := &mockPort{}

	// Create replacement mock for reopen
	reopenMock := &mockPort{}

	opts := &FlasherOptions{
		BaudRate:      115200,
		FlashBaudRate: 460800,
		ChipType:      ChipAuto,
		ResetMode:     ResetDefault,
		SerialOpener: func(name string, mode *serial.Mode) (serial.Port, error) {
			reopenCount.Add(1)
			return reopenMock, nil
		},
	}

	// Construct flasher directly (bypass New to avoid connect)
	f := &Flasher{
		port:    initialMock,
		conn:    newConn(initialMock),
		opts:    opts,
		portStr: "fake",
	}

	// Call reopenPort
	err := f.reopenPort()
	assert.NoError(t, err)

	// Verify opener was called during reopenPort
	assert.Equal(t, int32(1), reopenCount.Load())

	// Verify port was updated
	assert.Equal(t, reopenMock, f.port)
}
