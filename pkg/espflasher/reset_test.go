package espflasher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.bug.st/serial"
)

// recordingPort tracks all calls to SetDTR and SetRTS for testing.
// Each call is recorded as a separate event to allow testing the order and
// combinations of line state transitions.
type recordingPort struct {
	dtrCalls []bool
	rtsCalls []bool
}

func (r *recordingPort) SetDTR(dtr bool) error {
	r.dtrCalls = append(r.dtrCalls, dtr)
	return nil
}

func (r *recordingPort) SetRTS(rts bool) error {
	r.rtsCalls = append(r.rtsCalls, rts)
	return nil
}

func (r *recordingPort) Write(p []byte) (int, error)                           { return len(p), nil }
func (r *recordingPort) Read(p []byte) (int, error)                            { return 0, nil }
func (r *recordingPort) SetMode(mode *serial.Mode) error                       { return nil }
func (r *recordingPort) SetReadTimeout(t time.Duration) error                  { return nil }
func (r *recordingPort) SetWriteTimeout(t time.Duration) error                 { return nil }
func (r *recordingPort) Close() error                                          { return nil }
func (r *recordingPort) ResetInputBuffer() error                               { return nil }
func (r *recordingPort) ResetOutputBuffer() error                              { return nil }
func (r *recordingPort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }
func (r *recordingPort) Break(t time.Duration) error                           { return nil }
func (r *recordingPort) Drain() error                                          { return nil }

// TestClassicReset verifies the classic reset sequence.
func TestClassicReset(t *testing.T) {
	port := &recordingPort{}
	classicReset(port, defaultResetDelay)

	// Classic reset sequence:
	// 1. SetDTR(false), SetRTS(true)   - IO0=HIGH, EN=LOW (hold in reset)
	// 2. SetDTR(true), SetRTS(false)   - IO0=LOW, EN=HIGH (bootloader)
	// 3. SetDTR(false)                 - IO0=HIGH

	// Verify we have DTR calls (SetDTR is called 3 times)
	assert.GreaterOrEqual(t, len(port.dtrCalls), 2, "should call SetDTR multiple times")

	// Verify we have RTS calls (SetRTS is called 2 times)
	assert.GreaterOrEqual(t, len(port.rtsCalls), 2, "should call SetRTS multiple times")

	// Verify first DTR is false (IO0=HIGH)
	assert.Equal(t, false, port.dtrCalls[0], "first SetDTR should be false")

	// Verify first RTS is true (EN=LOW, chip held in reset)
	assert.Equal(t, true, port.rtsCalls[0], "first SetRTS should be true")

	// Verify second DTR is true (IO0=LOW for bootloader mode)
	assert.Equal(t, true, port.dtrCalls[1], "second SetDTR should be true")

	// Verify second RTS is false (EN=HIGH, release reset)
	assert.Equal(t, false, port.rtsCalls[1], "second SetRTS should be false")
}

// TestUnixTightReset verifies the Unix tight reset sequence.
func TestUnixTightReset(t *testing.T) {
	port := &recordingPort{}
	unixTightReset(port, defaultResetDelay)

	// UnixTightReset sequence using setDTRandRTS:
	// 1. setDTRandRTS(false, false) - IO0=HIGH, EN=HIGH
	// 2. setDTRandRTS(true, true)   - IO0=LOW, EN=LOW
	// 3. setDTRandRTS(false, true)  - IO0=HIGH, EN=LOW
	// 4. setDTRandRTS(true, false)  - IO0=LOW, EN=HIGH (bootloader mode)
	// 5. setDTRandRTS(false, false) - IO0=HIGH, EN=HIGH
	// 6. SetDTR(false)

	// Should have multiple DTR and RTS calls
	assert.GreaterOrEqual(t, len(port.dtrCalls), 4, "should have multiple DTR calls")
	assert.GreaterOrEqual(t, len(port.rtsCalls), 4, "should have multiple RTS calls")

	// Verify bootloader mode is reached (DTR=true, RTS=false at indices matching)
	// Look for the pattern where DTR becomes true before RTS becomes false
	dtrTrueIdx := -1
	rtsFalseIdx := -1
	for i, val := range port.dtrCalls {
		if val && dtrTrueIdx == -1 {
			dtrTrueIdx = i
		}
	}
	for i, val := range port.rtsCalls {
		if !val && rtsFalseIdx == -1 {
			rtsFalseIdx = i
		}
	}

	assert.NotEqual(t, -1, dtrTrueIdx, "should set DTR=true")
	assert.NotEqual(t, -1, rtsFalseIdx, "should set RTS=false")
}

// TestTightReset verifies the tight reset sequence.
func TestTightReset(t *testing.T) {
	port := &recordingPort{}
	tightReset(port, defaultResetDelay)

	// TightReset sequence:
	// 1. SetDTR(false), SetRTS(false)   - IO0=HIGH, EN=HIGH
	// 2. SetDTR(true), SetRTS(true)    - IO0=LOW, EN=LOW
	// 3. SetDTR(false), SetRTS(false)  - IO0=HIGH, EN=HIGH (release)

	assert.GreaterOrEqual(t, len(port.dtrCalls), 2, "should have at least 2 DTR calls")
	assert.GreaterOrEqual(t, len(port.rtsCalls), 2, "should have at least 2 RTS calls")

	// Verify initial state
	assert.Equal(t, false, port.dtrCalls[0], "first SetDTR should be false")
	assert.Equal(t, false, port.rtsCalls[0], "first SetRTS should be false")
}

// TestSetDTRandRTS verifies the setDTRandRTS helper.
func TestSetDTRandRTS(t *testing.T) {
	port := &recordingPort{}
	err := setDTRandRTS(port, true, false)
	require.NoError(t, err)

	// Should call SetDTR(true) and SetRTS(false)
	assert.True(t, len(port.dtrCalls) > 0, "should call SetDTR")
	assert.True(t, len(port.rtsCalls) > 0, "should call SetRTS")
	assert.Equal(t, true, port.dtrCalls[len(port.dtrCalls)-1], "last DTR call should be true")
	assert.Equal(t, false, port.rtsCalls[len(port.rtsCalls)-1], "last RTS call should be false")
}

// TestSetDTRandRTSBothTrue verifies setting both high.
func TestSetDTRandRTSBothTrue(t *testing.T) {
	port := &recordingPort{}
	err := setDTRandRTS(port, true, true)
	require.NoError(t, err)

	assert.Equal(t, true, port.dtrCalls[len(port.dtrCalls)-1], "last DTR call should be true")
	assert.Equal(t, true, port.rtsCalls[len(port.rtsCalls)-1], "last RTS call should be true")
}

// TestSetDTRandRTSBothFalse verifies setting both low.
func TestSetDTRandRTSBothFalse(t *testing.T) {
	port := &recordingPort{}
	err := setDTRandRTS(port, false, false)
	require.NoError(t, err)

	assert.Equal(t, false, port.dtrCalls[len(port.dtrCalls)-1], "last DTR call should be false")
	assert.Equal(t, false, port.rtsCalls[len(port.rtsCalls)-1], "last RTS call should be false")
}

// TestResetDelayConstants verifies the reset delay constants.
func TestResetDelayConstants(t *testing.T) {
	// Verify constants match esptool expectations
	assert.Equal(t, 50*time.Millisecond, defaultResetDelay, "defaultResetDelay should be 50ms")
	assert.Equal(t, 550*time.Millisecond, extraResetDelay, "extraResetDelay should be 550ms")
}
