package espflasher

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.bug.st/serial"
)

// recordingPort tracks all calls to SetDTR and SetRTS for testing.
// Separate dtrCalls/rtsCalls slices preserve the per-line value history;
// the unified calls slice preserves the full cross-line ordering needed
// by tests that assert on interleaving.
type recordingPort struct {
	dtrCalls []bool
	rtsCalls []bool
	calls    []lineCall
}

type lineCall struct {
	line  string // "DTR" or "RTS"
	value bool
}

func (r *recordingPort) SetDTR(dtr bool) error {
	r.dtrCalls = append(r.dtrCalls, dtr)
	r.calls = append(r.calls, lineCall{line: "DTR", value: dtr})
	return nil
}

func (r *recordingPort) SetRTS(rts bool) error {
	r.rtsCalls = append(r.rtsCalls, rts)
	r.calls = append(r.calls, lineCall{line: "RTS", value: rts})
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
func (r *recordingPort) GetModemStatusBits() (*serial.ModemStatusBits, error)  { return nil, nil }
func (r *recordingPort) Break(t time.Duration) error                           { return nil }
func (r *recordingPort) Drain() error                                          { return nil }

func indexOf(calls []lineCall, line string, value bool, startAt int) int {
	for i := startAt; i < len(calls); i++ {
		if calls[i].line == line && calls[i].value == value {
			return i
		}
	}
	return -1
}

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

// TestHardResetNonUSBReleasesDTRBeforeReleasingReset verifies that on the
// non-USB path, hardReset deasserts DTR before releasing EN (RTS=false).
// Otherwise a leftover DTR=true from a prior operation holds IO0 LOW when
// EN goes HIGH and the chip re-enters the download-mode bootloader.
func TestHardResetNonUSBReleasesDTRBeforeReleasingReset(t *testing.T) {
	port := &recordingPort{}
	hardReset(port, false)

	rtsTrue := indexOf(port.calls, "RTS", true, 0)
	require := assert.New(t)
	require.GreaterOrEqual(rtsTrue, 0, "expected SetRTS(true) to pull EN LOW")

	dtrFalse := indexOf(port.calls, "DTR", false, rtsTrue)
	require.Greater(dtrFalse, rtsTrue, "SetDTR(false) must happen after EN is pulled LOW")

	rtsFalseFinal := indexOf(port.calls, "RTS", false, dtrFalse)
	require.Greater(rtsFalseFinal, dtrFalse,
		"final SetRTS(false) (release reset) must happen after SetDTR(false) so IO0 is HIGH when EN goes HIGH")
}

// TestHardResetUSBDeassertsDTRFirst verifies that on the USB-JTAG path,
// hardReset deasserts DTR before driving EN, so GPIO0 is HIGH (normal boot,
// not bootloader) at the moment the USB-JTAG peripheral latches the reset.
func TestHardResetUSBDeassertsDTRFirst(t *testing.T) {
	port := &recordingPort{}
	hardReset(port, true)

	assert.NotEmpty(t, port.calls)
	first := port.calls[0]
	assert.Equal(t, "DTR", first.line, "first call must be SetDTR on USB path")
	assert.False(t, first.value, "first SetDTR must be false (release GPIO0)")
}

// TestFlasherResetESP32S2UsesWatchdog verifies that Flasher.Reset() takes
// the chip's HardResetOTG hook (RTC watchdog) for an ESP32-S2 flasher over
// native USB-OTG, instead of the DTR/RTS hardResetUSB path used by chips
// with a USB-Serial-JTAG bridge.
func TestFlasherResetESP32S2UsesWatchdog(t *testing.T) {
	port := &recordingPort{}
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil // strap clear, force-download not set
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return nil
		},
	}
	f := &Flasher{
		conn:    mc,
		port:    port,
		opts:    &FlasherOptions{},
		chip:    defESP32S2,
		usesUSB: true,
	}

	f.Reset()

	assert.Empty(t, port.calls, "watchdog reset path must not toggle DTR/RTS")
}

// TestFlasherResetESP32S2WatchdogWriteFailureFallsBack verifies that when a
// watchdog register write fails, Flasher.Reset() falls back to the DTR/RTS
// hardResetUSB path instead of treating the failed watchdog arm as a
// successful reset.
func TestFlasherResetESP32S2WatchdogWriteFailureFallsBack(t *testing.T) {
	port := &recordingPort{}
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil // strap clear, force-download not set
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			if addr == esp32s2RTCCntlWDTConfig0 {
				return errors.New("write failed")
			}
			return nil
		},
	}
	f := &Flasher{
		conn:    mc,
		port:    port,
		opts:    &FlasherOptions{},
		chip:    defESP32S2,
		usesUSB: true,
	}

	f.Reset()

	assert.NotEmpty(t, port.calls, "watchdog write failure must fall back to DTR/RTS reset")
}

// TestFlasherResetUSBJTAGChipUnchanged verifies that chips using the
// USB-Serial-JTAG bridge (no HardResetOTG hook, e.g. S3/C3/C6/H2/C5) still
// take the existing DTR/RTS hardResetUSB path, unaffected by the S2
// watchdog-reset addition.
func TestFlasherResetUSBJTAGChipUnchanged(t *testing.T) {
	port := &recordingPort{}
	mc := &mockConnection{}
	f := &Flasher{
		conn:    mc,
		port:    port,
		opts:    &FlasherOptions{},
		chip:    defESP32S3,
		usesUSB: true,
	}

	f.Reset()

	assert.NotEmpty(t, port.calls, "USB-Serial-JTAG chips must still use the DTR/RTS reset path")
}

// TestFlasherResetClearsForceDownloadBoot verifies Reset() clears the
// force-download-boot bit; left set, the chip comes back up in download
// mode instead of running the application.
func TestFlasherResetClearsForceDownloadBoot(t *testing.T) {
	for _, tt := range []struct {
		name string
		chip *chipDef
		reg  uint32
		mask uint32
	}{
		{"esp32c3", defESP32C3, esp32c3RTCCntlOption1Reg, esp32c3RTCCntlForceDownloadBoot},
		{"esp32s3", defESP32S3, esp32s3RTCCntlOption1Reg, esp32s3RTCCntlForceDownloadBoot},
	} {
		t.Run(tt.name, func(t *testing.T) {
			type regWrite struct {
				addr, value, mask uint32
			}
			var writes []regWrite
			mc := &mockConnection{
				writeRegFunc: func(addr, value, mask, delayUS uint32) error {
					writes = append(writes, regWrite{addr, value, mask})
					return nil
				},
			}
			f := &Flasher{
				conn:    mc,
				port:    &recordingPort{},
				opts:    &FlasherOptions{},
				chip:    tt.chip,
				usesUSB: true,
			}

			f.Reset()

			assert.Contains(t, writes, regWrite{tt.reg, 0, tt.mask},
				"Reset must clear the force-download-boot bit")
		})
	}
}

// TestFlasherResetClearsForceDownloadBootBeforeStubReboot verifies the clear
// happens while the loader still answers commands.
func TestFlasherResetClearsForceDownloadBootBeforeStubReboot(t *testing.T) {
	var order []string
	mc := &mockConnection{
		stubMode: true,
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			if addr == esp32c3RTCCntlOption1Reg {
				order = append(order, "clear")
			}
			return nil
		},
		flashEndFunc: func(reboot bool) error {
			order = append(order, "flashEnd")
			return nil
		},
	}
	f := &Flasher{
		conn:    mc,
		port:    &recordingPort{},
		opts:    &FlasherOptions{},
		chip:    defESP32C3,
		usesUSB: true,
	}

	f.Reset()

	require.Equal(t, []string{"clear", "flashEnd"}, order)
}

// TestFlasherResetForceDownloadBootFailureIsNonFatal verifies a failed clear
// (e.g. secure download mode) doesn't stop the reset.
func TestFlasherResetForceDownloadBootFailureIsNonFatal(t *testing.T) {
	port := &recordingPort{}
	mc := &mockConnection{
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return errors.New("write failed")
		},
	}
	f := &Flasher{
		conn:    mc,
		port:    port,
		opts:    &FlasherOptions{},
		chip:    defESP32C3,
		usesUSB: true,
	}

	f.Reset()

	assert.NotEmpty(t, port.calls, "reset must still run when the clear fails")
}

// TestFlasherResetNoForceDownloadBootReg verifies chips without the register
// skip the clear instead of writing to address 0.
func TestFlasherResetNoForceDownloadBootReg(t *testing.T) {
	writes := 0
	mc := &mockConnection{
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			return nil
		},
	}
	f := &Flasher{
		conn: mc,
		port: &recordingPort{},
		opts: &FlasherOptions{},
		chip: defESP8266,
	}

	f.Reset()

	assert.Equal(t, 0, writes, "chips without the register must not be written to")
}

// TestFlasherResetKeepsStubRunning verifies Reset() ends the session with
// reboot=false. reboot=true means "reboot into the ROM bootloader", leaving
// the chip in download mode with a re-enumerated (stale) port.
func TestFlasherResetKeepsStubRunning(t *testing.T) {
	var reboots []bool
	port := &recordingPort{}
	mc := &mockConnection{
		stubMode: true,
		flashEndFunc: func(reboot bool) error {
			reboots = append(reboots, reboot)
			return nil
		},
	}
	f := &Flasher{
		conn:    mc,
		port:    port,
		opts:    &FlasherOptions{},
		chip:    defESP32C3,
		usesUSB: true,
	}

	f.Reset()

	require.Equal(t, []bool{false}, reboots, "must not reboot back into the ROM bootloader")
	assert.NotEmpty(t, port.calls, "the hardware reset must still run")
}

// failingPort fails its modem-control writes, as a stale fd does after the
// USB device re-enumerated.
type failingPort struct {
	recordingPort
	err error
}

func (f *failingPort) SetDTR(dtr bool) error {
	f.recordingPort.SetDTR(dtr) //nolint:errcheck
	return f.err
}

func (f *failingPort) SetRTS(rts bool) error {
	f.recordingPort.SetRTS(rts) //nolint:errcheck
	return f.err
}

// TestHardResetReportsError verifies hardReset surfaces a failed write while
// still running the full sequence, so EN isn't left asserted.
func TestHardResetReportsError(t *testing.T) {
	for _, usesUSB := range []bool{false, true} {
		port := &failingPort{err: errors.New("input/output error")}

		err := hardReset(port, usesUSB)

		assert.Error(t, err, "usesUSB=%v", usesUSB)
		assert.Equal(t, false, port.rtsCalls[len(port.rtsCalls)-1],
			"the sequence must run to completion and release EN")
	}
}

// TestFlasherResetReportsFailedReset verifies Reset() doesn't claim
// "Device reset." when the DTR/RTS writes never reached the device.
func TestFlasherResetReportsFailedReset(t *testing.T) {
	var logged []string
	f := &Flasher{
		conn: &mockConnection{},
		port: &failingPort{err: errors.New("input/output error")},
		opts: &FlasherOptions{Logger: loggerFunc(func(format string, args ...interface{}) {
			logged = append(logged, fmt.Sprintf(format, args...))
		})},
		chip:    defESP32C3,
		usesUSB: true,
	}

	f.Reset()

	require.NotEmpty(t, logged)
	last := logged[len(logged)-1]
	assert.Contains(t, last, "may not have been reset")
	assert.NotContains(t, last, "Device reset.")
}

// loggerFunc adapts a function to the Logger interface.
type loggerFunc func(format string, args ...interface{})

func (l loggerFunc) Logf(format string, args ...interface{}) { l(format, args...) }
