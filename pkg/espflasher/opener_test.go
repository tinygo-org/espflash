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

func TestNewClosesPortOnConnectError(t *testing.T) {
	// E1-13 regression (error path): connect() fails immediately (sync never
	// succeeds because Read always returns 0 bytes), and New must close the
	// port it opened before returning the error.
	mock := &mockPort{}

	opts := &FlasherOptions{
		BaudRate:        115200,
		ResetMode:       ResetNoReset,
		ConnectAttempts: 1,
		SerialOpener: func(name string, mode *serial.Mode) (serial.Port, error) {
			return mock, nil
		},
	}

	f, err := New("fake", opts)
	if err == nil {
		t.Fatal("expected connect() to fail (sync never succeeds), got nil error")
	}
	if f != nil {
		t.Fatal("expected nil Flasher on connect error")
	}
	// closeCalls is 2, not 1: connect()'s sync loop fails, triggers a
	// reopenPort() (which closes+reopens the same mock port) before the
	// retry also fails; New's deferred close then closes it again on the
	// way out. That reopen-close is pre-existing behavior, unrelated to
	// this fix — the point of this test is that New's own close still
	// fires (previously an explicit non-deferred call) on the error path.
	if mock.closeCalls != 2 {
		t.Errorf("port Close() calls = %d, want exactly 2 on connect error (1 from reopenPort retry, 1 from New's deferred close)", mock.closeCalls)
	}
}

func TestNewClosesPortOnConnectPanic(t *testing.T) {
	// E1-13 regression (panic path): connect() panics partway through (here,
	// triggered via a Write that panics as sync() sends its first command).
	// New's deferred close must still run during the panic unwind, closing
	// the port exactly once. The panic itself is deliberately NOT recovered
	// by New (no recover() added there) — only this test recovers it, since
	// New must not hide the panic from the caller.
	mock := &mockPort{
		writeFunc: func(p []byte) (int, error) {
			panic("simulated write panic during connect")
		},
	}

	opts := &FlasherOptions{
		BaudRate:        115200,
		ResetMode:       ResetNoReset,
		ConnectAttempts: 1,
		SerialOpener: func(name string, mode *serial.Mode) (serial.Port, error) {
			return mock, nil
		},
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected New() to panic (propagated from connect()), but it did not")
			}
		}()
		New("fake", opts) //nolint:errcheck
	}()

	if mock.closeCalls != 1 {
		t.Errorf("port Close() calls = %d, want exactly 1 after panic in connect()", mock.closeCalls)
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
