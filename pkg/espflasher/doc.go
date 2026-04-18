// Package espflasher provides a Go library for flashing firmware to Espressif
// ESP8266 and ESP32-family microcontrollers over a serial (UART) connection.
// It implements the serial bootloader protocol used by the ESP ROM bootloader,
// supporting the following chip families:
//
//   - ESP8266
//   - ESP32
//   - ESP32-S2
//   - ESP32-S3
//   - ESP32-C2 (ESP8684)
//   - ESP32-C3
//   - ESP32-C5
//   - ESP32-C6
//   - ESP32-H2
//
// # Quick Start
//
// To flash a .bin file to a connected ESP device:
//
//	flasher, err := espflasher.New("/dev/ttyUSB0", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer flasher.Close()
//
//	data, _ := os.ReadFile("firmware.bin")
//	err = flasher.FlashImage(data, 0x0, nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	flasher.Reset()
//
// # Architecture
//
// The library is organized in layers:
//
//   - SLIP: Serial Line Internet Protocol framing (slip.go)
//   - Protocol: ROM bootloader command/response protocol (protocol.go)
//   - Chip: Per-target chip definitions and detection (chip.go, target_*.go)
//   - Image: Firmware image header parsing and patching (image.go)
//   - Stub: Stub loader for advanced operations like erase and read (stub.go)
//   - Flasher: High-level flash/verify/reset API (flasher.go)
//
// The protocol uses SLIP framing over serial UART. Commands are sent as
// request packets with an opcode, and the device responds with status.
// Flash writes can optionally use zlib-compressed data for faster transfers.
package espflasher // import "tinygo.org/x/espflasher/pkg/espflasher"
