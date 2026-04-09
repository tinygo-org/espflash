package espflasher

import (
	"bytes"
	"testing"
)

func TestESP32S3PostConnectUSBJTAG(t *testing.T) {
	var buf bytes.Buffer
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			if addr == esp32s3UARTDevBufNo {
				return esp32s3UARTDevBufNoUSBJTAGSerial, nil
			}
			// Return 0 for SWD conf read
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return nil
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{Logger: &StdoutLogger{W: &buf}},
	}

	err := esp32s3PostConnect(f)
	if err != nil {
		t.Fatalf("esp32s3PostConnect() error: %v", err)
	}
	if !f.usesUSB {
		t.Error("usesUSB should be true for USB-JTAG/Serial")
	}
}

func TestESP32S3PostConnectUSBOTG(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			if addr == esp32s3UARTDevBufNo {
				return esp32s3UARTDevBufNoUSBOTG, nil
			}
			return 0, nil
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32s3PostConnect(f)
	if err != nil {
		t.Fatalf("esp32s3PostConnect() error: %v", err)
	}
	if !f.usesUSB {
		t.Error("usesUSB should be true for USB-OTG")
	}
}

func TestESP32S3PostConnectUART(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil // Not USB
		},
	}
	f := &Flasher{
		conn: mc,
		opts: &FlasherOptions{},
	}

	err := esp32s3PostConnect(f)
	if err != nil {
		t.Fatalf("esp32s3PostConnect() error: %v", err)
	}
	if f.usesUSB {
		t.Error("usesUSB should be false for UART")
	}
}
