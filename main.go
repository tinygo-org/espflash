// Command espflasher flashes firmware to Espressif ESP8266 and ESP32-family
// microcontrollers over a serial (UART) connection.
//
// Install:
//
//	go install tinygo.org/x/espflasher@latest
//
// Usage:
//
//	espflasher -port /dev/ttyUSB0 -offset 0x0 firmware.bin
//	espflasher -port /dev/ttyUSB0 -bootloader bootloader.bin -partitions partitions.bin -app app.bin
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"tinygo.org/x/espflasher/pkg/espflasher"
)

func main() {
	port := flag.String("port", "", "Serial port (e.g. /dev/ttyUSB0, COM3)")
	baud := flag.Int("baud", 460800, "Flash baud rate")
	offset := flag.String("offset", "0x0", "Flash offset for single binary mode")
	chip := flag.String("chip", "auto", "Chip type: auto, esp8266, esp32, esp32s2, esp32s3, esp32c2, esp32c3, esp32c5, esp32c6, esp32h2")
	noCompress := flag.Bool("no-compress", false, "Disable compression")
	eraseAll := flag.Bool("erase-all", false, "Erase entire flash before writing")
	flashMode := flag.String("fm", "keep", "Flash mode: keep, qio, qout, dio, dout")
	flashFreq := flag.String("ff", "keep", "Flash frequency: keep, 80m, 40m, 26m, 20m (chip-specific)")
	flashSize := flag.String("fs", "keep", "Flash size: keep, 1MB, 2MB, 4MB, 8MB, 16MB")
	resetMode := flag.String("reset", "default", "Reset mode: default, no-reset, usb-jtag, auto")
	version := flag.Bool("version", false, "Print version and exit")

	// Multi-image mode
	bootloader := flag.String("bootloader", "", "Bootloader .bin file (multi-image mode)")
	partitions := flag.String("partitions", "", "Partition table .bin file (multi-image mode)")
	app := flag.String("app", "", "Application .bin file (multi-image mode)")
	bootloaderOffset := flag.String("bootloader-offset", "", "Bootloader offset (default: auto-detect from chip)")
	partitionsOffset := flag.String("partitions-offset", "0x8000", "Partition table offset")
	appOffset := flag.String("app-offset", "0x10000", "Application offset")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [firmware.bin]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Flash ESP32 family devices via serial port.")
		fmt.Fprintln(os.Stderr, "Flags can appear before or after the firmware file argument.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Single binary mode:")
		fmt.Fprintf(os.Stderr, "  %s -port /dev/ttyUSB0 firmware.bin\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -port /dev/ttyUSB0 firmware.bin -fm dout\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Multi-image mode:")
		fmt.Fprintf(os.Stderr, "  %s -port /dev/ttyUSB0 -bootloader bl.bin -partitions pt.bin -app app.bin\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}

	// Parse all arguments, allowing flags after positional args.
	// Go's flag package stops at the first non-flag argument, so we
	// pre-process os.Args to move the positional .bin file to the end.
	reorderArgs()
	flag.Parse()

	if *version {
		fmt.Printf("espflasher %s\n", espflasher.Version)
		return
	}

	if *port == "" {
		fmt.Fprintln(os.Stderr, "Error: -port is required")
		flag.Usage()
		os.Exit(1)
	}

	// Determine mode
	multiImage := *bootloader != "" || *partitions != "" || *app != ""
	singleImage := flag.NArg() > 0

	if !multiImage && !singleImage {
		fmt.Fprintln(os.Stderr, "Error: provide a firmware .bin file or use -bootloader/-partitions/-app flags")
		flag.Usage()
		os.Exit(1)
	}

	if multiImage && singleImage {
		fmt.Fprintln(os.Stderr, "Error: cannot use both single binary and multi-image mode")
		os.Exit(1)
	}

	opts := espflasher.DefaultOptions()
	opts.FlashBaudRate = *baud
	opts.Compress = !*noCompress
	opts.Logger = &espflasher.StdoutLogger{W: os.Stdout}
	opts.ChipType = parseChipType(*chip)
	opts.FlashMode = *flashMode
	opts.FlashFreq = *flashFreq
	opts.FlashSize = *flashSize
	switch strings.ToLower(*resetMode) {
	case "default":
		opts.ResetMode = espflasher.ResetDefault
	case "no-reset":
		opts.ResetMode = espflasher.ResetNoReset
	case "usb-jtag":
		opts.ResetMode = espflasher.ResetUSBJTAG
	case "auto":
		opts.ResetMode = espflasher.ResetAuto
	default:
		log.Fatalf("Unknown reset mode: %s", *resetMode)
	}

	flasher, err := espflasher.New(*port, opts)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer func() {
		if err := flasher.Close(); err != nil {
			log.Printf("Warning: failed to close flasher: %v", err)
		}
	}()

	fmt.Printf("Chip: %s\n", flasher.ChipName())

	if *eraseAll {
		if err := flasher.EraseFlash(); err != nil {
			log.Fatalf("Erase failed: %v", err)
		}
	}

	progress := func(current, total int) {
		pct := float64(current) / float64(total) * 100
		bar := int(pct / 2)
		fmt.Printf("\r[%-50s] %6.1f%%", strings.Repeat("#", bar)+strings.Repeat(".", 50-bar), pct)
		if current >= total {
			fmt.Println()
		}
	}

	if singleImage {
		filename := flag.Arg(0)
		data, err := os.ReadFile(filename)
		if err != nil {
			log.Fatalf("Read %s: %v", filename, err)
		}

		off := parseHex(*offset)
		fmt.Printf("Writing %s (%d bytes) at 0x%08X...\n", filename, len(data), off)

		if err := flasher.FlashImage(data, off, progress); err != nil {
			log.Fatalf("Flash failed: %v", err)
		}
	} else {
		var images []espflasher.ImagePart

		if *bootloader != "" {
			data, err := os.ReadFile(*bootloader)
			if err != nil {
				log.Fatalf("Read bootloader: %v", err)
			}
			off := parseHex(*bootloaderOffset)
			if *bootloaderOffset == "" {
				// Use chip-specific default
				off = 0x1000 // Most common default
			}
			images = append(images, espflasher.ImagePart{Data: data, Offset: off})
			fmt.Printf("Bootloader: %s (%d bytes) at 0x%08X\n", *bootloader, len(data), off)
		}

		if *partitions != "" {
			data, err := os.ReadFile(*partitions)
			if err != nil {
				log.Fatalf("Read partitions: %v", err)
			}
			off := parseHex(*partitionsOffset)
			images = append(images, espflasher.ImagePart{Data: data, Offset: off})
			fmt.Printf("Partitions: %s (%d bytes) at 0x%08X\n", *partitions, len(data), off)
		}

		if *app != "" {
			data, err := os.ReadFile(*app)
			if err != nil {
				log.Fatalf("Read app: %v", err)
			}
			off := parseHex(*appOffset)
			images = append(images, espflasher.ImagePart{Data: data, Offset: off})
			fmt.Printf("App: %s (%d bytes) at 0x%08X\n", *app, len(data), off)
		}

		if err := flasher.FlashImages(images, progress); err != nil {
			log.Fatalf("Flash failed: %v", err)
		}
	}

	fmt.Println("Resetting device...")
	flasher.Reset()
	fmt.Println("Done!")
}

func parseHex(s string) uint32 {
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	val, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		log.Fatalf("Invalid hex value: %s", s)
	}
	return uint32(val)
}

func parseChipType(s string) espflasher.ChipType {
	switch strings.ToLower(s) {
	case "auto":
		return espflasher.ChipAuto
	case "esp8266":
		return espflasher.ChipESP8266
	case "esp32":
		return espflasher.ChipESP32
	case "esp32s2", "esp32-s2":
		return espflasher.ChipESP32S2
	case "esp32s3", "esp32-s3":
		return espflasher.ChipESP32S3
	case "esp32c2", "esp32-c2":
		return espflasher.ChipESP32C2
	case "esp32c3", "esp32-c3":
		return espflasher.ChipESP32C3
	case "esp32c5", "esp32-c5":
		return espflasher.ChipESP32C5
	case "esp32c6", "esp32-c6":
		return espflasher.ChipESP32C6
	case "esp32h2", "esp32-h2":
		return espflasher.ChipESP32H2
	default:
		log.Fatalf("Unknown chip type: %s", s)
		return espflasher.ChipAuto
	}
}

// reorderArgs moves positional arguments (non-flag args that aren't values of
// a flag) to the end of os.Args so that Go's flag package can parse all flags
// regardless of where they appear on the command line. This lets users write:
//
//	espflasher -port COM3 firmware.bin -fm dout
//
// instead of requiring all flags before the positional argument.
func reorderArgs() {
	if len(os.Args) <= 1 {
		return
	}

	// Collect known flags that take a value argument (not booleans).
	// We need this to know which args following a flag are its values.
	valueFlagNames := map[string]bool{}
	boolFlagNames := map[string]bool{}
	flag.VisitAll(func(f *flag.Flag) {
		// Check if the flag's default is a boolean type by looking at the
		// zero value. Bool flags have IsBoolFlag() method.
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			boolFlagNames[f.Name] = true
		} else {
			valueFlagNames[f.Name] = true
		}
	})

	args := os.Args[1:]
	var flagArgs []string
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// Everything after "--" is positional
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			// Check if this flag takes a value
			name := strings.TrimLeft(arg, "-")
			// Handle -flag=value syntax
			if eqIdx := strings.Index(name, "="); eqIdx >= 0 {
				continue // value is already part of this arg
			}
			if valueFlagNames[name] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}

	os.Args = append([]string{os.Args[0]}, append(flagArgs, positional...)...)
}
