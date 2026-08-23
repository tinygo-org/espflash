package espflasher

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUSBInterfaceFromPort(t *testing.T) {
	tests := []struct {
		name     string
		vid, pid string
		found    bool
		chip     *chipDef
		want     usbInterfaceKind
	}{
		{"usb-serial-jtag", "303A", "1001", true, defESP32C3, usbInterfaceSerialJTAG},
		{"lower case vid/pid", "303a", "1001", true, defESP32C3, usbInterfaceSerialJTAG},
		{"usb-otg s3", "303A", "0009", true, defESP32S3, usbInterfaceOTG},
		{"usb-otg s2", "303A", "0002", true, defESP32S2, usbInterfaceOTG},
		{"otg pid of another chip", "303A", "0002", true, defESP32S3, usbInterfaceUnknown},
		{"otg pid without chip", "303A", "0009", true, nil, usbInterfaceUnknown},
		{"usb-uart bridge", "10C4", "EA60", true, defESP32C3, usbInterfaceUnknown},
		{"port not found", "", "", false, defESP32C3, usbInterfaceUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubPortVIDPID(t, tt.vid, tt.pid, tt.found)
			f := &Flasher{chip: tt.chip, portStr: "/dev/ttyACM0"}
			assert.Equal(t, tt.want, f.usbInterfaceFromPort())
		})
	}
}

// TestNormalizePortName covers matching a port against the enumerated list.
func TestNormalizePortName(t *testing.T) {
	// macOS exposes the same device as both cu.* and tty.*.
	assert.True(t, samePortName("/dev/cu.usbmodem1101", "/dev/tty.usbmodem1101"))
	assert.True(t, samePortName("/dev/ttyACM0", "ttyACM0"))
	assert.True(t, samePortName("COM3", "com3"))
	assert.False(t, samePortName("/dev/ttyACM0", "/dev/ttyACM1"))
	assert.False(t, samePortName("/dev/ttyUSB0", "/dev/ttyACM0"))
}

// TestESP32S3PostConnectUSBJTAGByVIDPID verifies the S3 takes the
// USB-Serial/JTAG branch on a VID/PID match, even when the register reads as
// a UART.
func TestESP32S3PostConnectUSBJTAGByVIDPID(t *testing.T) {
	stubPortVIDPID(t, espressifUSBVID, usbSerialJTAGPID, true)
	writes := 0
	mc := &mockConnection{
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			return nil
		},
	}
	f := &Flasher{conn: mc, opts: &FlasherOptions{}, chip: defESP32S3, portStr: "/dev/ttyACM0"}

	err := esp32s3PostConnect(f)
	assert.NoError(t, err)
	assert.True(t, f.usesUSB)
	assert.Greater(t, writes, 0, "USB-Serial/JTAG must disable the watchdogs")
}

// TestESP32S3PostConnectOTGByVIDPID verifies the OTG PID takes the USB-OTG
// branch, which leaves the watchdogs alone.
func TestESP32S3PostConnectOTGByVIDPID(t *testing.T) {
	stubPortVIDPID(t, espressifUSBVID, "0009", true)
	writes := 0
	mc := &mockConnection{
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			return nil
		},
	}
	f := &Flasher{conn: mc, opts: &FlasherOptions{}, chip: defESP32S3, portStr: "/dev/ttyACM0"}

	err := esp32s3PostConnect(f)
	assert.NoError(t, err)
	assert.True(t, f.usesUSB)
	assert.Equal(t, 0, writes, "USB-OTG must not touch the watchdogs")
}

// TestESP32S2PostConnectOTGByVIDPID verifies USB-OTG detection by VID/PID.
func TestESP32S2PostConnectOTGByVIDPID(t *testing.T) {
	stubPortVIDPID(t, espressifUSBVID, "0002", true)
	f := &Flasher{
		conn:    &mockConnection{},
		opts:    &FlasherOptions{},
		chip:    defESP32S2,
		portStr: "/dev/ttyACM0",
	}

	err := esp32s2PostConnect(f)
	assert.NoError(t, err)
	assert.True(t, f.usesUSB)
}
