# espflasher

[![PkgGoDev](https://pkg.go.dev/badge/pkg.go.dev/tinygo.org/x/espflasher)](https://pkg.go.dev/tinygo.org/x/espflasher) [![Test](https://github.com/tinygo-org/espflasher/actions/workflows/test.yml/badge.svg)](https://github.com/tinygo-org/espflasher/actions/workflows/test.yml)

A Go command-line tool and library for flashing firmware to Espressif ESP8266 and ESP32-family microcontrollers over serial (UART) or USB-JTAG/Serial connections.

## Supported Chips

- ESP8266
- ESP32
- ESP32-S2
- ESP32-S3
- ESP32-C2 (ESP8684)
- ESP32-C3
- ESP32-C5
- ESP32-C6
- ESP32-H2

## CLI Tool

### Installation

You can download and install one of the prebuilt binaries for your operating system under "Releases" or install from source:

```bash
go install tinygo.org/x/espflasher@latest
```

### CLI Usage

```bash
# Install
go install tinygo.org/x/espflasher@latest

# Flash a single binary
espflasher -port /dev/ttyUSB0 firmware.bin

# Flash with specific offset and chip
espflasher -port /dev/ttyUSB0 -offset 0x10000 -chip esp32s3 app.bin

# Flash via native USB-JTAG/Serial (ESP32-S3, ESP32-C3, etc.)
espflasher -port /dev/ttyACM0 -reset usb-jtag -chip esp32s3 firmware.bin

# Flash multiple images (bootloader + partitions + app)
espflasher -port /dev/ttyUSB0 \
    -bootloader bootloader.bin \
    -partitions partitions.bin \
    -app application.bin

# Erase flash before writing
espflasher -port /dev/ttyUSB0 -erase-all firmware.bin
```

## Library

### Installation

```bash
go get tinygo.org/x/espflasher/pkg/espflasher
```

### Quick Start

```go
package main

import (
    "fmt"
    "log"
    "os"

    "tinygo.org/x/espflasher/pkg/espflasher"
)

func main() {
    // Connect to the ESP device
    flasher, err := espflasher.New("/dev/ttyUSB0", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer flasher.Close()

    fmt.Printf("Connected to %s\n", flasher.ChipName())

    // Read the firmware binary
    data, err := os.ReadFile("firmware.bin")
    if err != nil {
        log.Fatal(err)
    }

    // Flash with progress reporting
    err = flasher.FlashImage(data, 0x0, func(current, total int) {
        fmt.Printf("\rFlashing: %d/%d bytes (%.0f%%)", current, total,
            float64(current)/float64(total)*100)
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println()

    // Reset the device to run the new firmware
    flasher.Reset()
    fmt.Println("Done!")
}
```

## Features

- **Auto-detection**: Automatically identifies the connected ESP chip
- **Compressed transfers**: Uses zlib compression for significantly faster flashing
- **Multi-image support**: Flash bootloader, partition table, and application in one operation
- **Progress callbacks**: Monitor flash progress in real-time
- **MD5 verification**: Verifies written data integrity after flashing
- **Configurable**: Customize baud rate, compression, reset mode, and more
- **USB-JTAG/Serial**: Native USB support for boards like ESP32-S3 and ESP32-C3 that expose a built-in USB-JTAG/Serial interface (typically `/dev/ttyACM0` on Linux, `cu.usbmodem*` on macOS)
- **Stubs**: Use stubs for higher-speed downloads and other advanced processor features
- **NVS support**: Generate and parse ESP-IDF NVS (Non-Volatile Storage) partition images in pure Go

## NVS Package

The `nvs` package provides a pure-Go implementation for generating and parsing [ESP-IDF NVS (Non-Volatile Storage)](https://docs.espressif.com/projects/esp-idf/en/stable/esp32/api-reference/storage/nvs_flash.html) partition images in the v2 binary format.

### Installation

```bash
go get tinygo.org/x/espflasher/pkg/nvs
```

### Generating an NVS Partition

```go
import "tinygo.org/x/espflasher/pkg/nvs"

entries := []nvs.Entry{
    {Namespace: "wifi", Key: "ssid", Type: "string", Value: "MyNetwork"},
    {Namespace: "wifi", Key: "channel", Type: "u8", Value: uint8(6)},
    {Namespace: "config", Key: "timeout", Type: "u16", Value: uint16(3000)},
    {Namespace: "config", Key: "name", Type: "string", Value: "MyDevice"},
}

partition, err := nvs.GenerateNVS(entries, nvs.DefaultPartSize)
if err != nil {
    log.Fatal(err)
}
// partition is a []byte ready to flash at the NVS partition offset
```

### Parsing an NVS Partition

```go
entries, err := nvs.ParseNVS(partitionData)
if err != nil {
    log.Fatal(err)
}
for _, e := range entries {
    fmt.Printf("[%s] %s = %v\n", e.Namespace, e.Key, e.Value)
}
```

### Supported Value Types

| Type | Go Value Type |
|------|---------------|
| `u8` | `uint8` (or `int`) |
| `u16` | `uint16` (or `int`) |
| `u32` | `uint32` (or `int`) |
| `i8` | `int8` (or `int`) |
| `i16` | `int16` (or `int`) |
| `i32` | `int32` (or `int`) |
| `string` | `string` |
| `blob` | `[]byte` |

## Reset Modes

| CLI Flag | Go Constant | Description |
|----------|-------------|-------------|
| `default` | `ResetDefault` | Classic DTR/RTS reset sequence. Works with most boards using a USB-to-UART bridge (CP2102, CH340, etc.). |
| `usb-jtag` | `ResetUSBJTAG` | Reset sequence for boards with a native USB-JTAG/Serial interface (ESP32-S3, ESP32-C3, ESP32-C6, ESP32-H2). Use this when connected via `/dev/ttyACM0` (Linux) or `cu.usbmodem*` (macOS). |
| `no-reset` | `ResetNoReset` | Skip hardware reset entirely. Useful when the chip is already in bootloader mode or reset is handled externally. |

## API Overview

### Creating a Flasher

```go
// With default options (115200 baud, auto-detect, compressed)
flasher, err := espflasher.New("/dev/ttyUSB0", nil)

// With custom options
opts := espflasher.DefaultOptions()
opts.FlashBaudRate = 921600
opts.ChipType = espflasher.ChipESP32S3
opts.Logger = &espflasher.StdoutLogger{W: os.Stdout}
flasher, err := espflasher.New("/dev/ttyUSB0", opts)

// For boards with native USB-JTAG/Serial (ESP32-S3, ESP32-C3, etc.)
opts.ResetMode = espflasher.ResetUSBJTAG
flasher, err := espflasher.New("/dev/ttyACM0", opts)
```

### Flashing a Single Binary

```go
data, _ := os.ReadFile("firmware.bin")
err := flasher.FlashImage(data, 0x0, progressCallback)
```

### Flashing Multiple Images

```go
images := []espflasher.ImagePart{
    {Data: bootloaderBin, Offset: 0x1000},
    {Data: partitionsBin, Offset: 0x8000},
    {Data: applicationBin, Offset: 0x10000},
}
err := flasher.FlashImages(images, progressCallback)
```

### Other Operations

```go
// Erase entire flash
err := flasher.EraseFlash()

// Erase a specific region (must be sector-aligned)
err := flasher.EraseRegion(0x10000, 0x100000)

// Read a hardware register
val, err := flasher.ReadRegister(0x3FF00050)

// Hard reset the device
flasher.Reset()
```

## Architecture

The library is organized in layers:

| Layer | File(s) | Description |
|-------|---------|-------------|
| SLIP | `pkg/espflasher/slip.go` | Serial Line Internet Protocol framing |
| Protocol | `pkg/espflasher/protocol.go` | ROM bootloader command/response protocol |
| Chip | `pkg/espflasher/chip.go`, `pkg/espflasher/target_*.go` | Per-target definitions and detection |
| Reset | `pkg/espflasher/reset.go` | Hardware reset strategies |
| Flasher | `pkg/espflasher/flasher.go` | High-level flash/verify/reset API |
| NVS | `pkg/nvs/*.go` | Generate and parse NVS partition images |
| CLI | `main.go` | Command-line interface |

## Development

### Updating Stubs

The flasher includes pre-compiled bootloader stubs from [esp-flasher-stub](https://github.com/espressif/esp-flasher-stub) releases. To update stubs:

1. Edit `STUB_VERSION` in `tools/update-stubs.sh` to the desired release version
2. Run `go generate ./pkg/espflasher/...` to download and embed the latest stubs
3. The `go:generate` directive in `pkg/espflasher/stub.go` will invoke `tools/update-stubs.sh`

## Protocol Reference

This library implements the ESP serial bootloader protocol as documented by Espressif's [esptool](https://github.com/espressif/esptool). Key protocol features:

- SLIP framing (RFC 1055) over UART
- 8-byte command/response headers with opcodes
- XOR checksum verification
- Compressed flash writes via zlib deflate
- MD5 verification of flash contents
- SPI flash parameter configuration
