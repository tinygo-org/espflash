package espflasher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPortVIDPID replaces USB port enumeration for the duration of a test.
func stubPortVIDPID(t *testing.T, vid, pid string, ok bool) {
	t.Helper()
	prev := portVIDPID
	portVIDPID = func(string) (string, string, bool) { return vid, pid, ok }
	t.Cleanup(func() { portVIDPID = prev })
}

func TestESP32C3PostConnectUSBJTAG(t *testing.T) {
	stubPortVIDPID(t, "", "", false) // host reports no VID/PID
	writeCount := 0

	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			if addr == esp32c3UARTDevBufNo {
				return esp32c3UARTDevBufNoUSBJTAGSerial, nil
			}
			// Return 0 for SWD conf read
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writeCount++
			return nil
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32c3PostConnect(f)
	require.NoError(t, err)
	assert.True(t, f.usesUSB, "usesUSB should be true for USB-JTAG/Serial")
	assert.Greater(t, writeCount, 0, "should have written registers to disable watchdog")
}

// TestESP32C3PostConnectUSBJTAGByVIDPID verifies a VID/PID match is detected
// even when UARTDEV_BUF_NO reads back a non-USB value, e.g. because this ROM
// revision keeps the variable elsewhere.
func TestESP32C3PostConnectUSBJTAGByVIDPID(t *testing.T) {
	stubPortVIDPID(t, espressifUSBVID, usbSerialJTAGPID, true)
	readCount := 0

	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			readCount++
			return 0, nil // UART value, and 0 for the SWD conf read
		},
	}
	f := &Flasher{
		conn:    mc,
		opts:    &FlasherOptions{},
		portStr: "/dev/ttyACM0",
	}

	err := esp32c3PostConnect(f)
	require.NoError(t, err)
	assert.True(t, f.usesUSB, "VID/PID match should detect USB-JTAG/Serial")
	assert.NotEqual(t, 0, readCount, "should still disable watchdogs")
}

func TestESP32C3PostConnectUART(t *testing.T) {
	stubPortVIDPID(t, "10C4", "EA60", true) // CP2102 USB-UART bridge
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil // Not USB, return UART value
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32c3PostConnect(f)
	require.NoError(t, err)
	assert.False(t, f.usesUSB, "usesUSB should be false for UART")
}

func TestESP32C3PostConnectReadError(t *testing.T) {
	stubPortVIDPID(t, "", "", false)
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, errors.New("secure download mode")
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32c3PostConnect(f)
	require.NoError(t, err, "an unreadable register must fall back to non-USB, not fail")
	assert.False(t, f.usesUSB)
}

func TestESP32C3MAC(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			switch addr {
			case esp32c3EfuseBlock1Word0:
				return 0x60559ff7, nil
			case esp32c3EfuseBlock1Word0 + 4:
				return 0x00002ca2, nil
			}
			return 0, nil
		},
	}
	f := &Flasher{conn: mc, chip: defESP32C3}

	mac, err := f.MAC()
	require.NoError(t, err)
	assert.Equal(t, "2c:a2:60:55:9f:f7", mac.String())
}

func TestESP32C3ChipRevision(t *testing.T) {
	tests := []struct {
		name  string
		word3 uint32
		word5 uint32
		want  ChipRevision
	}{
		{"v0.0", 0x00000000, 0x00000000, ChipRevision{0, 0}},
		{"v1.0", 0x00000000, 0x01000000, ChipRevision{1, 0}},
		{"v1.1", 0x00040000, 0x01000000, ChipRevision{1, 1}},
		{"max", 0x001c0000, 0x03800000, ChipRevision{3, 15}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &mockConnection{
				readRegFunc: func(addr uint32) (uint32, error) {
					switch addr {
					case esp32c3EfuseBlock1Word3:
						return tt.word3, nil
					case esp32c3EfuseBlock1Word5:
						return tt.word5, nil
					}
					return 0, nil
				},
			}
			f := &Flasher{conn: mc, chip: defESP32C3}
			got, err := f.ChipRevision()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestESP32C3ChipFeatures(t *testing.T) {
	tests := []struct {
		name  string
		word3 uint32
		word4 uint32
		want  []string
	}{
		{"no flash", 0, 0, []string{"Wi-Fi", "BT 5 (LE)", "Single Core", "160MHz"}},
		{"4MB XMC", 1 << 27, 1, []string{"Wi-Fi", "BT 5 (LE)", "Single Core", "160MHz", "Embedded Flash 4MB (XMC)"}},
		{"8MB unknown vendor", 4 << 27, 0, []string{"Wi-Fi", "BT 5 (LE)", "Single Core", "160MHz", "Embedded Flash 8MB ()"}},
		{"unknown cap", 6 << 27, 0, []string{"Wi-Fi", "BT 5 (LE)", "Single Core", "160MHz", "Unknown Embedded Flash ()"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &mockConnection{
				readRegFunc: func(addr uint32) (uint32, error) {
					switch addr {
					case esp32c3EfuseBlock1Word3:
						return tt.word3, nil
					case esp32c3EfuseBlock1Word4:
						return tt.word4, nil
					}
					return 0, nil
				},
			}
			f := &Flasher{conn: mc, chip: defESP32C3}
			got, err := f.ChipFeatures()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestESP32C3ReadErrorsPropagate(t *testing.T) {
	newF := func(readReg func(addr uint32) (uint32, error)) *Flasher {
		return &Flasher{conn: &mockConnection{readRegFunc: readReg}, chip: defESP32C3}
	}
	assertRegisterErrorsPropagate(t, newF, []uint32{esp32c3EfuseBlock1Word0, esp32c3EfuseBlock1Word0 + 4},
		func(f *Flasher) error { _, err := f.MAC(); return err })
	assertRegisterErrorsPropagate(t, newF, []uint32{esp32c3EfuseBlock1Word3, esp32c3EfuseBlock1Word5},
		func(f *Flasher) error { _, err := f.ChipRevision(); return err })
	assertRegisterErrorsPropagate(t, newF, []uint32{esp32c3EfuseBlock1Word3, esp32c3EfuseBlock1Word4},
		func(f *Flasher) error { _, err := f.ChipFeatures(); return err })
}

func TestESP32C3PostConnectSecureMode(t *testing.T) {
	// Simulate read error (secure download mode)
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, errors.New("register not readable") // Simulate unreadable register
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32c3PostConnect(f)
	require.NoError(t, err, "should gracefully handle read error")
	assert.False(t, f.usesUSB, "should default to non-USB on read error")
}
