package espflasher

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"time"

	"go.bug.st/serial"
)

// FlasherOptions configures the Flasher behavior.
type FlasherOptions struct {
	// BaudRate is the initial baud rate for serial communication.
	// Default: 115200.
	BaudRate int

	// FlashBaudRate is the baud rate used during flash data transfer.
	// If set and higher than BaudRate, the flasher will switch to this rate
	// after connecting. Set to 0 to keep the initial baud rate.
	// Default: 460800.
	FlashBaudRate int

	// ChipType forces a specific chip type instead of auto-detection.
	// Default: ChipAuto (auto-detect).
	ChipType ChipType

	// ResetMode controls how the chip is reset to enter bootloader.
	// Default: ResetDefault.
	ResetMode ResetMode

	// ConnectAttempts is the number of connection attempts before failing.
	// Default: 7.
	ConnectAttempts int

	// Compress enables zlib compression for flash data transfer.
	// Significantly faster for large images. Requires stub loader or
	// ESP32+ ROM bootloader.
	// Default: true.
	Compress bool

	// FlashMode sets the SPI flash access mode in the image header.
	// Valid values: "qio", "qout", "dio", "dout".
	// Empty string or "keep" preserves the value from the binary.
	// Most ESP32 boards work with "dio"; some need "dout".
	FlashMode string

	// FlashFreq sets the SPI flash clock frequency in the image header.
	// Valid values are chip-specific, e.g. "80m", "40m", "26m", "20m".
	// Empty string or "keep" preserves the value from the binary.
	FlashFreq string

	// FlashSize sets the flash chip size in the image header.
	// Valid values: "1MB", "2MB", "4MB", "8MB", "16MB", etc.
	// Empty string or "keep" preserves the value from the binary.
	FlashSize string

	// Logger receives informational messages during flashing.
	// If nil, messages are discarded silently.
	Logger Logger

	// ConnectStatus, if non-nil, receives status updates during connection setup.
	ConnectStatus ConnectStatusFunc

	// SerialOpener, if non-nil, is used instead of serial.Open for all
	// port opens (initial and reopen-after-USB-reenumeration). Useful for
	// callers that multiplex port access across a monitor and the flasher.
	SerialOpener func(name string, mode *serial.Mode) (serial.Port, error)

	// SkipStub, when true, skips uploading the stub loader during connect.
	// Stub-dependent features (whole-chip erase, compression, etc.) are
	// unavailable, but ROM operations (register read/write, flash read/write
	// via ROM, chip detect) still work. Useful for register-only workflows
	// (e.g. GPIO probing) that don't need the stub, and for avoiding a
	// resident stub that can cause a subsequent no-reset reconnect to
	// mis-detect the chip.
	// Default: false (stub is loaded as today).
	SkipStub bool
}

// Logger is the interface for receiving progress and status messages.
type Logger interface {
	// Logf logs a formatted informational message.
	Logf(format string, args ...interface{})
}

// ProgressFunc is called with progress updates during long-running
// operations. The meaning of current and total is operation-defined: for
// flashing and reading, they are bytes transferred and total bytes; for
// erase and GetFlashMD5, they are elapsed and estimated-total milliseconds,
// since both are single blocking commands with no per-chunk protocol seam
// to report exact byte progress — the device stays silent for the duration
// and reports no real completion percentage.
type ProgressFunc func(current, total int)

// ConnectPhase identifies a stage of the bootloader connection sequence.
type ConnectPhase string

const (
	ConnectPhaseReset      ConnectPhase = "reset"
	ConnectPhaseSync       ConnectPhase = "sync"
	ConnectPhaseDetectChip ConnectPhase = "detect_chip"
	ConnectPhaseLoadStub   ConnectPhase = "load_stub"
)

// ConnectStatusFunc receives status updates during the Flasher connection
// setup sequence (reset, sync, chip detection, stub load). For phases with a
// bounded retry loop (reset/sync), attempt and maxAttempts report progress
// through that loop; phases without a natural retry count pass 0 for both.
// message is a short human-readable description. It is best-effort and must
// never affect connection success.
type ConnectStatusFunc func(phase ConnectPhase, attempt, maxAttempts int, message string)

// DefaultOptions returns FlasherOptions with sensible defaults.
func DefaultOptions() *FlasherOptions {
	return &FlasherOptions{
		BaudRate:        115200,
		FlashBaudRate:   460800,
		ChipType:        ChipAuto,
		ResetMode:       ResetDefault,
		ConnectAttempts: 7,
		Compress:        true,
	}
}

// openPort opens a serial port, using SerialOpener if available.
func (opts *FlasherOptions) openPort(name string, mode *serial.Mode) (serial.Port, error) {
	if opts != nil && opts.SerialOpener != nil {
		return opts.SerialOpener(name, mode)
	}
	return serial.Open(name, mode)
}

// connection defines the low-level protocol operations for communicating
// with an ESP bootloader over a serial connection.
type connection interface {
	sync() (uint32, error)
	readReg(addr uint32) (uint32, error)
	writeReg(addr, value, mask, delayUS uint32) error
	securityInfo() ([]byte, error)
	flashBegin(size, offset uint32, encrypted bool) error
	flashData(block []byte, seq uint32) error
	flashEnd(reboot bool) error
	flashDeflBegin(uncompSize, compSize, offset uint32, encrypted bool) error
	flashDeflData(block []byte, seq uint32) error
	flashDeflEnd(reboot bool) error
	flashMD5(addr, size uint32) ([]byte, error)
	flashWriteSize() uint32
	spiAttach(value uint32) error
	spiSetParams(totalSize, blockSize, sectorSize, pageSize uint32) error
	changeBaud(newBaud, oldBaud uint32) error
	eraseFlash() error
	eraseRegion(offset, size uint32) error
	readFlash(offset, size uint32, progress ProgressFunc) ([]byte, error)
	flushInput()
	isStub() bool
	setUSB(v bool)
	setSupportsEncryptedFlash(v bool)
	loadStub(s *stub) error
}

// Flasher manages the connection to an ESP device and provides
// high-level flash operations.
type Flasher struct {
	port    serial.Port
	conn    connection
	chip    *chipDef
	opts    *FlasherOptions
	portStr string
	usesUSB bool
	secInfo []byte // cached security info from ROM (GET_SECURITY_INFO opcode 0x14)
}

// New creates a new Flasher connected to the given serial port.
//
// It opens the serial port, enters the bootloader, syncs with the device,
// and detects the chip type. On success, the Flasher is ready for flash
// operations.
//
// If opts is nil, DefaultOptions() is used.
//
// Example:
//
//	f, err := espflasher.New("/dev/ttyUSB0", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer f.Close()
func New(portName string, opts *FlasherOptions) (*Flasher, error) {
	if opts == nil {
		opts = DefaultOptions()
	}

	mode := &serial.Mode{
		BaudRate: opts.BaudRate,
		Parity:   serial.NoParity,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}

	port, err := opts.openPort(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", portName, err)
	}

	f := &Flasher{
		port:    port,
		conn:    newConn(port),
		opts:    opts,
		portStr: portName,
	}

	// Ensure the port is closed on any non-success return from here on,
	// including a panic unwinding through f.connect(). succeeded is only
	// set true right before the final successful return.
	succeeded := false
	defer func() {
		if !succeeded {
			f.port.Close() //nolint:errcheck
		}
	}()

	// Connect to the bootloader
	if err := f.connect(); err != nil {
		return nil, err
	}

	succeeded = true
	return f, nil
}

// Close releases the serial port and associated resources.
func (f *Flasher) Close() error {
	return f.port.Close()
}

// FlushInput discards any unread data from the serial port and SLIP reader.
// Useful when reusing a flasher connection across multiple operations.
func (f *Flasher) FlushInput() {
	f.conn.flushInput()
}

// reopenPort closes and reopens the serial port after a USB device
// re-enumeration. TinyUSB CDC devices may briefly disappear during reset.
func (f *Flasher) reopenPort() error {
	f.port.Close() //nolint:errcheck

	var lastErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		port, err := f.opts.openPort(f.portStr, &serial.Mode{
			BaudRate: f.opts.BaudRate,
			Parity:   serial.NoParity,
			DataBits: 8,
			StopBits: serial.OneStopBit,
		})
		if err == nil {
			f.port = port
			f.conn = newConn(port)
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("reopen port %s: %w", f.portStr, lastErr)
}

// connectViaUSBJTAG performs a USB-JTAG/Serial reset and attempts to
// reconnect. The reset causes the USB device to disconnect and
// re-enumerate, so the old port is closed and a new one is opened.
//
// The reset sequence is run in a goroutine because ioctls on a
// disconnected cdc_acm fd can block for several seconds on Linux.
// After a short timeout we close the old port (which unblocks the
// goroutine) and wait for the device to re-enumerate.
func (f *Flasher) connectViaUSBJTAG() bool {
	done := make(chan struct{})
	go func() {
		usbJTAGSerialReset(f.port)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		// Reset likely triggered but post-disconnect ioctls are blocking.
		// reopenPort will close the fd, unblocking the goroutine.
	}
	if err := f.reopenPort(); err != nil {
		return false
	}
	f.conn.flushInput()
	for range 5 {
		_, err := f.conn.sync()
		if err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ChipType returns the detected chip type.
func (f *Flasher) ChipType() ChipType {
	if f.chip != nil {
		return f.chip.ChipType
	}
	return ChipAuto
}

// ChipName returns the detected chip name (e.g. "ESP32-S3").
func (f *Flasher) ChipName() string {
	if f.chip != nil {
		return f.chip.Name
	}
	return "Unknown"
}

// BootloaderFlashOffset returns the flash offset where the bootloader image
// lives for the connected chip. Returns (0, false) if the chip has not been
// detected yet (e.g. connect() has not completed).
func (f *Flasher) BootloaderFlashOffset() (uint32, bool) {
	if f.chip == nil {
		return 0, false
	}
	return f.chip.BootloaderFlashOffset, true
}

// connect performs the bootloader connection sequence:
// reset → sync → detect chip.
func (f *Flasher) connect() error {
	f.logf("Connecting to %s...", f.portStr)

	attempts := f.opts.ConnectAttempts
	if attempts <= 0 {
		attempts = 7
	}

	for attempt := 0; attempt < attempts; attempt++ {
		f.connectStatus(ConnectPhaseReset, attempt+1, attempts, "entering download mode")

		// Reset the chip into bootloader mode
		switch f.opts.ResetMode {
		case ResetDefault:
			// On Unix systems (darwin, linux), use the esptool sequence:
			// unixTightReset with 50ms, then 550ms, then fallback to classic resets
			if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
				switch attempt % 4 {
				case 0:
					unixTightReset(f.port, defaultResetDelay)
				case 1:
					unixTightReset(f.port, extraResetDelay)
				case 2:
					classicReset(f.port, defaultResetDelay)
				case 3:
					classicReset(f.port, extraResetDelay)
				}
			} else {
				// On other systems, use classic and tight reset alternation
				if attempt%2 == 0 {
					classicReset(f.port, defaultResetDelay)
				} else {
					tightReset(f.port, defaultResetDelay)
				}
			}
		case ResetUSBJTAG:
			if f.connectViaUSBJTAG() {
				goto synced
			}
			continue
		case ResetAuto:
			switch attempt % 3 {
			case 0:
				classicReset(f.port, defaultResetDelay)
			case 1:
				if f.connectViaUSBJTAG() {
					goto synced
				}
				continue
			case 2:
				// No reset — device may already be in bootloader
			}
		}
		// ResetNoReset: skip reset entirely

		// Try to sync with the bootloader
		f.connectStatus(ConnectPhaseSync, attempt+1, attempts, "syncing")
		time.Sleep(100 * time.Millisecond) // Give bootloader time to start
		f.conn.flushInput()
		for range 5 {
			_, err := f.conn.sync()
			if err == nil {
				goto synced
			}
			time.Sleep(50 * time.Millisecond)
		}

		// Sync failed — try reopening port (USB CDC may have re-enumerated
		// after reset, causing the old port handle to be stale).
		if err := f.reopenPort(); err != nil {
			continue // port reopen failed, try next attempt
		}

		// Port reopened successfully — the device likely just finished
		// USB re-enumeration after the reset above and the bootloader
		// is already waiting for commands. Try to sync now before
		// looping back and issuing another reset (which would disconnect
		// USB all over again).
		f.conn.flushInput()
		for range 5 {
			_, err := f.conn.sync()
			if err == nil {
				goto synced
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	return &SyncError{Attempts: attempts}

synced:
	f.logf("Connected.")

	// Detect chip type
	if f.opts.ChipType == ChipAuto {
		f.connectStatus(ConnectPhaseDetectChip, 0, 0, "detecting chip")
		chip, err := f.detectChip()
		if err != nil {
			return err
		}
		f.chip = chip
	} else {
		def, ok := chipDefs[f.opts.ChipType]
		if !ok {
			return fmt.Errorf("unsupported chip type: %s", f.opts.ChipType)
		}
		f.chip = def
	}

	f.logf("Detected chip: %s", f.chip.Name)

	// Run chip-specific post-connect initialization.
	if f.chip.PostConnect != nil {
		if err := f.chip.PostConnect(f); err != nil {
			f.logf("Warning: post-connect: %v", err)
		}
	}

	// Propagate chip capabilities to the connection layer.
	f.conn.setSupportsEncryptedFlash(f.chip.SupportsEncryptedFlash)

	// Propagate USB flag to connection layer for block size optimization.
	if f.usesUSB {
		f.conn.setUSB(true)
	}

	// Upload the stub loader to enable advanced features (erase, compression, etc.).
	if f.opts.SkipStub {
		f.logf("Skipping stub loader (SkipStub set); ROM bootloader only.")
	} else if s, ok := stubFor(f.chip.ChipType); ok {
		f.logf("Loading stub loader...")
		f.connectStatus(ConnectPhaseLoadStub, 0, 0, "loading stub")
		if err := f.conn.loadStub(s); err != nil {
			f.logf("Warning: could not load stub: %v", err)
		} else {
			f.logf("Stub running.")
		}
	}

	return nil
}

// detectChip identifies the connected ESP chip.
func (f *Flasher) detectChip() (*chipDef, error) {
	si, err := f.readSecurityInfo()
	if err != nil {
		f.logf("unable to read security info: %s", err)
	}

	for _, def := range chipDefs {
		if def.UsesMagicValue {
			continue
		}

		if si != nil && si.ChipID != nil && *si.ChipID == uint32(def.ImageChipID) {
			def.SecureDownloadMode = si.ParsedFlags.SecureDownloadEnable
			return def, nil
		}
	}

	// Otherwise, try to read the chip magic value to verify the chip type (ESP8266, ESP32, ESP32-S2)
	magic, err := f.conn.readReg(chipDetectMagicRegAddr)
	if err != nil {
		// Only ESP32-S2 does not support chip id detection
		// and supports secure download mode
		f.logf("unable to read chip magic value. Defaulting to ESP32-S2: %s", err)

		// si may be nil here if readSecurityInfo also failed above; default
		// SecureDownloadMode to false in that case rather than dereferencing.
		// Note: chipDefs[ChipESP32S2] is a shared package-level global being
		// mutated in place here (pre-existing behavior, not changed by this fix).
		secureDownload := false
		if si != nil {
			secureDownload = si.ParsedFlags.SecureDownloadEnable
		}
		chipDefs[ChipESP32S2].SecureDownloadMode = secureDownload
		return chipDefs[ChipESP32S2], nil
	}

	// Check magic value for older chips (ESP8266, ESP32, ESP32-S2)
	if def := detectChipByMagic(magic); def != nil {
		return def, nil
	}

	return nil, &ChipDetectError{MagicValue: magic}
}

// FlashImage writes a firmware image to flash at the given offset.
//
// The data should be a raw .bin file (not ELF). The offset is typically
// 0x0 for a merged/combined binary, or a specific address like 0x10000
// for the application partition.
//
// If progress is non-nil, it will be called periodically with the number
// of bytes transferred so far.
//
// Example:
//
//	data, _ := os.ReadFile("firmware.bin")
//	err := f.FlashImage(data, 0x0, func(cur, total int) {
//	    fmt.Printf("\r%d/%d bytes", cur, total)
//	})
func (f *Flasher) FlashImage(data []byte, offset uint32, progress ProgressFunc) error {
	if len(data) == 0 {
		return fmt.Errorf("empty image data")
	}

	// Auto-detect flash size when not explicitly set.
	// This matches esptool.py's default --flash_size=detect behavior.
	// On ESP8266, the ROM bootloader uses the flash size from the image
	// header to configure SPI flash memory mapping, so it must be correct
	// for firmware to execute properly.
	if (f.opts.FlashSize == "" || f.opts.FlashSize == "keep") &&
		len(data) >= 4 && data[0] == espImageMagic {
		if detected := f.detectFlashSize(); detected != "" {
			f.logf("Configuring flash size...")
			f.logf("Auto-detected flash size: %s", detected)
			f.opts.FlashSize = detected
		}
	}

	// ESP8266 only supports DOUT (and DIO on some boards). Force DOUT
	// when no explicit flash mode was requested to ensure the image boots.
	if f.chip != nil && f.chip.ChipType == ChipESP8266 &&
		(f.opts.FlashMode == "" || f.opts.FlashMode == "keep") {
		f.opts.FlashMode = "dout"
	}

	// Patch flash parameters in image header (mode, frequency, size)
	var err error
	data, err = f.patchImageHeader(data)
	if err != nil {
		return fmt.Errorf("patch image header: %w", err)
	}

	// Pad data to 4-byte alignment
	if pad := len(data) % 4; pad != 0 {
		data = append(data, make([]byte, 4-pad)...)
	}

	// Attach SPI flash if not already done
	if err := f.attachFlash(); err != nil {
		return fmt.Errorf("attach flash: %w", err)
	}

	// Optionally switch to higher baud rate (not supported by ESP8266 ROM)
	canChangeBaud := f.chip == nil || f.chip.ROMHasChangeBaud || f.conn.isStub()
	if canChangeBaud && f.opts.FlashBaudRate > 0 && f.opts.FlashBaudRate != f.opts.BaudRate {
		if err := f.changeBaud(f.opts.FlashBaudRate); err != nil {
			f.logf("Warning: could not change baud rate to %d: %v", f.opts.FlashBaudRate, err)
			// Continue at original baud rate
		}
	}

	// Use compressed flash only with the stub loader. While most ESP32+ ROM
	// bootloaders support compressed flash commands, we must skip flashDeflEnd
	// for ROM (it exits the bootloader), which can leave data unflushed in
	// the ROM's decompressor buffer. esptool also defaults to uncompressed
	// writes for ROM (compress = IS_STUB).
	if f.opts.Compress && f.conn.isStub() {
		return f.flashCompressed(data, offset, progress)
	}
	return f.flashUncompressed(data, offset, progress)
}

// FlashImages writes multiple firmware images to flash at their respective offsets.
// This is useful for flashing bootloader + partition table + application in one go.
//
// Each entry is a (data, offset) pair.
//
// Example:
//
//	images := []espflasher.ImagePart{
//	    {Data: bootloader, Offset: 0x1000},
//	    {Data: partTable, Offset: 0x8000},
//	    {Data: app, Offset: 0x10000},
//	}
//	err := f.FlashImages(images, progress)
func (f *Flasher) FlashImages(images []ImagePart, progress ProgressFunc) error {
	// Attach SPI flash first
	if err := f.attachFlash(); err != nil {
		return fmt.Errorf("attach flash: %w", err)
	}

	// Auto-detect flash size when not explicitly set, then re-push the SPI
	// params so the stub knows the real chip size. Without the re-push, the
	// stub keeps its default 4 MB bound from the initial attachFlash() and
	// rejects any write past 4 MB with ESP_FAILED_SPI_OP (status 0xC4).
	if f.opts.FlashSize == "" || f.opts.FlashSize == "keep" {
		if detected := f.detectFlashSize(); detected != "" {
			f.logf("Configuring flash size...")
			f.logf("Auto-detected flash size: %s", detected)
			f.opts.FlashSize = detected
		}
	}
	if f.chip == nil || f.chip.ChipType != ChipESP8266 {
		if err := f.configureSPIForSize(); err != nil {
			return err
		}
	}

	// ESP8266 only supports DOUT (and DIO on some boards). Force DOUT
	// when no explicit flash mode was requested to ensure the image boots.
	if f.chip != nil && f.chip.ChipType == ChipESP8266 &&
		(f.opts.FlashMode == "" || f.opts.FlashMode == "keep") {
		f.opts.FlashMode = "dout"
	}

	// Optionally switch to higher baud rate (not supported by ESP8266 ROM)
	canChangeBaud := f.chip == nil || f.chip.ROMHasChangeBaud || f.conn.isStub()
	if canChangeBaud && f.opts.FlashBaudRate > 0 && f.opts.FlashBaudRate != f.opts.BaudRate {
		if err := f.changeBaud(f.opts.FlashBaudRate); err != nil {
			f.logf("Warning: could not change baud rate to %d: %v", f.opts.FlashBaudRate, err)
		}
	}

	totalSize := 0
	for _, img := range images {
		totalSize += len(img.Data)
	}

	written := 0
	for _, img := range images {
		data := img.Data

		// Patch flash parameters in image header (mode, frequency, size)
		var err error
		data, err = f.patchImageHeader(data)
		if err != nil {
			return fmt.Errorf("patch image header at 0x%08X: %w", img.Offset, err)
		}

		// Pad to 4-byte alignment
		if pad := len(data) % 4; pad != 0 {
			data = append(data, make([]byte, 4-pad)...)
		}

		f.logf("Writing %d bytes at 0x%08X...", len(data), img.Offset)

		var partProgress ProgressFunc
		if progress != nil {
			base := written
			partProgress = func(cur, total int) {
				progress(base+cur, totalSize)
			}
		}

		if f.opts.Compress && f.conn.isStub() {
			err = f.flashCompressed(data, img.Offset, partProgress)
		} else {
			err = f.flashUncompressed(data, img.Offset, partProgress)
		}
		if err != nil {
			return fmt.Errorf("flash at 0x%08X: %w", img.Offset, err)
		}

		written += len(img.Data)
	}

	return nil
}

// ImagePart represents a firmware image segment with its flash offset.
type ImagePart struct {
	// Data is the raw binary data to flash.
	Data []byte

	// Offset is the flash address to write to (e.g. 0x0, 0x1000, 0x10000).
	Offset uint32
}

// flashCompressed writes data to flash using zlib compression.
func (f *Flasher) flashCompressed(data []byte, offset uint32, progress ProgressFunc) error {
	compressed, err := compressData(data)
	if err != nil {
		return fmt.Errorf("compress: %w", err)
	}

	uncompSize := uint32(len(data))
	compSize := uint32(len(compressed))
	f.logf("Compressed %d bytes to %d (%.0f%%)", uncompSize, compSize,
		float64(compSize)/float64(uncompSize)*100)

	// Only use compression if it actually helps
	if compSize >= uncompSize {
		f.logf("Compression not beneficial, using uncompressed write")
		return f.flashUncompressed(data, offset, progress)
	}

	writeSize := f.conn.flashWriteSize()
	numBlocks := (compSize + writeSize - 1) / writeSize

	f.logf("Flash begin: %d bytes at 0x%08X (%d compressed blocks)", compSize, offset, numBlocks)

	if err := f.conn.flashDeflBegin(uncompSize, compSize, offset, false); err != nil {
		return err
	}

	// Send compressed data blocks
	sent := uint32(0)
	seq := uint32(0)

	for sent < compSize {
		blockLen := min(compSize-sent, writeSize)

		block := compressed[sent : sent+blockLen]
		if err := f.retryFlashBlock(seq, numBlocks, func() error {
			return f.conn.flashDeflData(block, seq)
		}); err != nil {
			return fmt.Errorf("flash block %d of %d: %w", seq, numBlocks, err)
		}

		sent += blockLen
		seq++

		if progress != nil {
			// Map compressed bytes sent to approximate uncompressed progress
			approxUncomp := min(int(float64(sent)/float64(compSize)*float64(uncompSize)), int(uncompSize))
			progress(approxUncomp, int(uncompSize))
		}
	}

	// End the compressed flash session.
	// For ROM bootloaders, skip sending FLASH_DEFL_END — the ROM exits the
	// bootloader upon receiving it, which can interfere with flash operations.
	// esptool also skips this for ROM: "skip sending flash_finish to ROM loader,
	// as it causes the loader to exit and run user code."
	// For the stub, the end command acts as a write barrier: the stub uses
	// double-buffering (ACKs each block immediately, writes to flash as a
	// post-process), so the end command ensures the last block's flash write
	// has completed before we proceed.
	if f.conn.isStub() {
		if err := f.conn.flashDeflEnd(false); err != nil {
			return err
		}
	}

	f.logf("Flash complete. Verifying...")

	// Verify via MD5 if stub is running
	if f.conn.isStub() {
		if err := f.verifyMD5(data, offset); err != nil {
			return err
		}
	}

	return nil
}

// flashUncompressed writes data to flash without compression.
func (f *Flasher) flashUncompressed(data []byte, offset uint32, progress ProgressFunc) error {
	writeSize := f.conn.flashWriteSize()
	dataSize := uint32(len(data))
	numBlocks := (dataSize + writeSize - 1) / writeSize

	f.logf("Flash begin: %d bytes at 0x%08X (%d blocks)", dataSize, offset, numBlocks)

	if err := f.conn.flashBegin(dataSize, offset, false); err != nil {
		return err
	}

	sent := uint32(0)
	seq := uint32(0)

	for sent < dataSize {
		blockLen := dataSize - sent
		if blockLen > writeSize {
			blockLen = writeSize
		}

		block := data[sent : sent+blockLen]

		// Pad last block to writeSize
		if uint32(len(block)) < writeSize {
			padded := make([]byte, writeSize)
			copy(padded, block)
			for i := len(block); i < len(padded); i++ {
				padded[i] = 0xFF
			}
			block = padded
		}

		if err := f.retryFlashBlock(seq, numBlocks, func() error {
			return f.conn.flashData(block, seq)
		}); err != nil {
			return fmt.Errorf("flash block %d of %d: %w", seq, numBlocks, err)
		}

		sent += blockLen
		seq++

		if progress != nil {
			progress(int(sent), int(dataSize))
		}
	}

	// End the flash session.
	// For ROM bootloaders, skip sending FLASH_END — the ROM exits the
	// bootloader upon receiving it. esptool also skips this for ROM.
	// For the stub, the end command acts as a write barrier.
	if f.conn.isStub() {
		if err := f.conn.flashEnd(false); err != nil {
			return err
		}
	}

	f.logf("Flash complete. Verifying...")

	// Verify via MD5 if stub is running
	if f.conn.isStub() {
		if err := f.verifyMD5(data, offset); err != nil {
			return err
		}
	}

	return nil
}

// verifyMD5 checks the MD5 of the flashed data against the expected value.
func (f *Flasher) verifyMD5(data []byte, offset uint32) error {
	expected := md5.Sum(data)
	expectedHex := hex.EncodeToString(expected[:])

	actual, err := f.conn.flashMD5(offset, uint32(len(data)))
	if err != nil {
		f.logf("Warning: MD5 verification failed: %v", err)
		return nil // Don't fail on MD5 check failure
	}

	actualHex := hex.EncodeToString(actual)
	if actualHex != expectedHex {
		return fmt.Errorf("MD5 mismatch: expected %s, got %s", expectedHex, actualHex)
	}

	f.logf("MD5 verified: %s", actualHex)
	return nil
}

// EraseFlash erases the entire flash memory.
// This operation can take a significant amount of time (30-120 seconds).
// Requires the stub loader to be running.
//
// If progress is non-nil, it is called periodically with a synthetic ETA
// (elapsed and estimated milliseconds) ticked against the erase timeout,
// since the device stays silent for the duration of the erase and reports
// no real completion percentage. Pass nil to skip this entirely.
func (f *Flasher) EraseFlash(progress ProgressFunc) error {
	if !f.conn.isStub() {
		return &UnsupportedCommandError{Command: "erase flash (requires stub)"}
	}

	f.logf("Erasing flash...")
	if progress == nil {
		if err := f.conn.eraseFlash(); err != nil {
			return err
		}
		f.logf("Flash erased.")
		return nil
	}

	if err := tickErase(chipEraseTimeout, eraseProgressInterval, progress, f.conn.eraseFlash); err != nil {
		return err
	}
	f.logf("Flash erased.")
	return nil
}

// EraseRegion erases a region of flash memory.
// Requires the stub loader to be running.
// Both offset and size must be aligned to the flash sector size (4096 bytes).
//
// If progress is non-nil, it is called periodically with a synthetic ETA
// (elapsed and estimated milliseconds) ticked against the erase timeout,
// since the device stays silent for the duration of the erase and reports
// no real completion percentage. Pass nil to skip this entirely.
func (f *Flasher) EraseRegion(offset, size uint32, progress ProgressFunc) error {
	if !f.conn.isStub() {
		return &UnsupportedCommandError{Command: "erase region (requires stub)"}
	}

	if offset%flashSectorSize != 0 {
		return fmt.Errorf("offset 0x%X is not aligned to sector size 0x%X", offset, flashSectorSize)
	}
	if size%flashSectorSize != 0 {
		return fmt.Errorf("size 0x%X is not aligned to sector size 0x%X", size, flashSectorSize)
	}

	f.logf("Erasing %d bytes at 0x%08X...", size, offset)
	if progress == nil {
		return f.conn.eraseRegion(offset, size)
	}

	return tickErase(eraseTimeoutForSize(size), eraseProgressInterval, progress, func() error {
		return f.conn.eraseRegion(offset, size)
	})
}

// eraseProgressInterval is the tick interval used by tickErase when reporting
// synthetic erase progress via EraseFlash and EraseRegion.
const eraseProgressInterval = 500 * time.Millisecond

// flashBlockRetries is the number of attempts for each flash data block write.
// At high baud rates (460800+), USB-UART bridges occasionally lose bytes during
// transmission, causing the stub to receive a truncated or corrupted SLIP frame.
// The stub detects this (bad data length or bad checksum) and responds with a
// clean error, leaving it ready for a resend. We retry only for these
// serial-integrity errors; device-side failures (SPI errors, inflate errors)
// are not retried.
const flashBlockRetries = 3

// tickErase runs work (a blocking erase call) while emitting synthetic ETA
// progress updates against est every interval, until work returns. progress
// is called with (elapsedMs, estMs), capped so elapsed never reaches est
// until work has actually completed successfully. On success, a final
// progress(estMs, estMs) call is emitted; on error, no such call is made.
//
// The ticker goroutine only ever calls progress — it never touches the
// connection — and is always stopped and joined before tickErase returns,
// via a deferred cleanup registered before work is invoked. This holds even
// if work panics: the deferred cleanup still runs during unwinding, the
// ticker goroutine is joined, and the panic is left to propagate afterward
// without emitting a bogus final progress(estMs, estMs) call.
func tickErase(est, interval time.Duration, progress ProgressFunc, work func() error) error {
	estMs := int(est / time.Millisecond)

	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsedMs := int(time.Since(start) / time.Millisecond)
				if elapsedMs >= estMs {
					elapsedMs = estMs - 1
				}
				progress(elapsedMs, estMs)
			}
		}
	}()

	var success bool
	defer func() {
		close(done)
		<-stopped
		if success {
			progress(estMs, estMs)
		}
	}()

	if err := work(); err != nil {
		return err
	}
	success = true
	return nil
}

// ReadRegister reads a 32-bit register from the device.
func (f *Flasher) ReadRegister(addr uint32) (uint32, error) {
	return f.conn.readReg(addr)
}

// WriteRegister writes a 32-bit value to a register on the device.
func (f *Flasher) WriteRegister(addr, value uint32) error {
	return f.conn.writeReg(addr, value, 0xFFFFFFFF, 0)
}

// GetSecurityInfo returns security-related information from the device.
func (f *Flasher) GetSecurityInfo() (*SecurityInfo, error) {
	return f.readSecurityInfo()
}

// GetFlashMD5 returns the MD5 hash of a flash region.
// Requires the stub loader to be running.
//
// If progress is non-nil, it is called periodically with a synthetic ETA
// (elapsed and estimated milliseconds) ticked against the same size-scaled
// timeout used internally for the MD5 command, since the device stays
// silent for the duration of the hash computation and reports no real
// completion percentage. Pass nil to skip this entirely.
func (f *Flasher) GetFlashMD5(offset, size uint32, progress ProgressFunc) (string, error) {
	if !f.conn.isStub() {
		return "", &UnsupportedCommandError{Command: "flash MD5 (requires stub)"}
	}

	if err := f.attachFlash(); err != nil {
		return "", err
	}

	if progress == nil {
		result, err := f.conn.flashMD5(offset, size)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(result), nil
	}

	var result []byte
	err := tickMD5(md5TimeoutForSize(size), md5ProgressInterval, progress, func() error {
		r, err := f.conn.flashMD5(offset, size)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(result), nil
}

// md5ProgressInterval is the tick interval used by tickMD5 when reporting
// synthetic MD5 progress via GetFlashMD5.
const md5ProgressInterval = 500 * time.Millisecond

// tickMD5 runs work (a blocking flash-MD5 call) while emitting synthetic ETA
// progress updates against est every interval, until work returns. progress
// is called with (elapsedMs, estMs), capped so elapsed never reaches est
// until work has actually completed successfully. On success, a final
// progress(estMs, estMs) call is emitted; on error, no such call is made.
//
// Intermediate ETA ticks are best-effort: they run on a background ticker
// goroutine, so a panic from the caller's progress callback during such a
// tick is recovered and that tick is dropped silently rather than crashing
// the process. The final completion progress(estMs, estMs) call runs
// synchronously in the caller's goroutine (via the deferred cleanup below),
// so a panic there propagates to the caller normally.
//
// The ticker goroutine only ever calls progress — it never touches the
// connection — and is always stopped and joined before tickMD5 returns, via
// a deferred cleanup registered before work is invoked. This holds even if
// work panics: the deferred cleanup still runs during unwinding, the ticker
// goroutine is joined, and the panic is left to propagate afterward without
// emitting a bogus final progress(estMs, estMs) call.
func tickMD5(est, interval time.Duration, progress ProgressFunc, work func() error) error {
	estMs := int(est / time.Millisecond)
	if estMs < 1 {
		estMs = 1
	}

	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsedMs := int(time.Since(start) / time.Millisecond)
				if elapsedMs >= estMs {
					elapsedMs = estMs - 1
				}
				// Intermediate ticks are best-effort: a panicking
				// callback drops this tick but must not crash the
				// process or leak the ticker goroutine.
				func() {
					defer func() { _ = recover() }()
					progress(elapsedMs, estMs)
				}()
			}
		}
	}()

	var success bool
	defer func() {
		close(done)
		<-stopped
		if success {
			progress(estMs, estMs)
		}
	}()

	if err := work(); err != nil {
		return err
	}
	success = true
	return nil
}

// ReadFlash reads data from flash memory.
// Requires the stub loader to be running.
// If progress is non-nil, it is called after each block is read with the
// cumulative bytes read so far; the final call reports (size, size).
// If size is 0, the read returns immediately with no progress callback invoked.
func (f *Flasher) ReadFlash(offset, size uint32, progress ProgressFunc) ([]byte, error) {
	if !f.conn.isStub() {
		return nil, &UnsupportedCommandError{Command: "read flash (requires stub)"}
	}

	if err := f.attachFlash(); err != nil {
		return nil, err
	}

	data, err := f.conn.readFlash(offset, size, progress)
	// Clear any stale data left in the serial buffer and SLIP reader
	// after the raw block-read protocol. Without this, leftover bytes
	// can corrupt subsequent command responses.
	f.conn.flushInput()
	return data, err
}

// Reset performs a hard reset of the device, causing it to run user code.
func (f *Flasher) Reset() {
	if f.conn.isStub() {
		// Tell the stub to cleanly exit flash mode and reboot.
		// flashEnd(true) triggers a software reboot inside the stub.
		// For ROM bootloaders, skip flash_begin/flash_end — sending
		// CMD_FLASH_BEGIN after a compressed download may interfere with
		// the flash controller state at offset 0.
		f.conn.flashBegin(0, 0, false) //nolint:errcheck
		f.conn.flashEnd(true)          //nolint:errcheck
		time.Sleep(50 * time.Millisecond)
	}

	// Chips with a chip-specific hard reset (e.g. the ESP32-S2 USB-OTG
	// watchdog reset) use it here; if it's unavailable or returns false,
	// fall through to the DTR/RTS toggling below.
	if f.chip != nil && f.chip.HardResetOTG != nil && f.chip.HardResetOTG(f) {
		f.logf("Device reset.")
		return
	}

	if f.usesUSB {
		hardResetUSB(f.port)
	} else {
		hardReset(f.port, false)
	}
	f.logf("Device reset.")
}

// attachFlash attaches the SPI flash and configures parameters.
// ESP8266 does not need (or support) SPI attach; its ROM handles flash directly.
func (f *Flasher) attachFlash() error {
	if f.chip != nil && f.chip.ChipType == ChipESP8266 {
		return nil // ESP8266 ROM handles flash internally
	}

	f.logf("Attaching SPI flash...")

	if err := f.conn.spiAttach(0); err != nil {
		return err
	}

	return f.configureSPIForSize()
}

// configureSPIForSize tells the stub the flash chip's actual size derived
// from f.opts.FlashSize. Without this, the stub keeps the default 4 MB
// bound and rejects any write past 4 MB with ESP_FAILED_SPI_OP (status
// 0xC4). Falls back to 4 MB when the size string is empty / "keep" /
// unrecognised, matching the previous behaviour for the unconfigured case.
func (f *Flasher) configureSPIForSize() error {
	sizeBytes := uint32(4 * 1024 * 1024)
	if b, ok := flashSizeStringToBytes(f.opts.FlashSize); ok {
		sizeBytes = b
	}
	err := f.conn.spiSetParams(
		sizeBytes,
		64*1024, // 64KB block
		4*1024,  // 4KB sector
		256,     // 256B page
	)
	if err != nil {
		// Don't fail - some ROM versions don't support this.
		f.logf("Warning: SPI params config failed (may be OK): %v", err)
	}
	return nil
}

// flashSizeStringToBytes converts a flash size string (e.g. "8MB") to the
// chip capacity in bytes. Returns (0, false) for empty / "keep" / unknown
// inputs so callers can apply a default.
func flashSizeStringToBytes(s string) (uint32, bool) {
	switch s {
	case "1MB":
		return 1 * 1024 * 1024, true
	case "2MB":
		return 2 * 1024 * 1024, true
	case "4MB":
		return 4 * 1024 * 1024, true
	case "8MB":
		return 8 * 1024 * 1024, true
	case "16MB":
		return 16 * 1024 * 1024, true
	case "32MB":
		return 32 * 1024 * 1024, true
	case "64MB":
		return 64 * 1024 * 1024, true
	case "128MB":
		return 128 * 1024 * 1024, true
	}
	return 0, false
}

// changeBaud switches to a higher baud rate for faster data transfer.
func (f *Flasher) changeBaud(newBaud int) error {
	f.logf("Switching to %d baud...", newBaud)

	oldBaud := uint32(f.opts.BaudRate)
	if err := f.conn.changeBaud(uint32(newBaud), oldBaud); err != nil {
		return err
	}

	// Change the local port baud rate
	time.Sleep(50 * time.Millisecond)
	if err := f.port.SetMode(&serial.Mode{
		BaudRate: newBaud,
		Parity:   serial.NoParity,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}); err != nil {
		return fmt.Errorf("set local baud rate: %w", err)
	}

	time.Sleep(50 * time.Millisecond)
	f.conn.flushInput()

	f.logf("Running at %d baud.", newBaud)
	return nil
}

// compressData compresses data using zlib (best compression).
func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		w.Close() //nolint:errcheck
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// logf logs a message if a logger is configured.
func (f *Flasher) logf(format string, args ...interface{}) {
	if f.opts.Logger != nil {
		f.opts.Logger.Logf(format, args...)
	}
}

// retryFlashBlock calls writeFn up to flashBlockRetries times, retrying on
// errors that indicate transient serial data loss. Two cases are retried:
//
//  1. CommandError with IsRetryable() (bad data length / bad checksum): the
//     stub received a corrupted frame and responded with a clean error. It is
//     ready for a resend.
//
//  2. TimeoutError: the stub never responded, likely because bytes were lost
//     during UART transmission, leaving the stub waiting for the rest of an
//     incomplete SLIP frame. On resend, the leading 0xC0 terminates the stub's
//     partial frame; the stub may then emit a stale error response for the
//     truncated frame before processing our retry, which is handled by the
//     next retry iteration.
//
// Between retries the serial RX buffer and SLIP reader are flushed so stale
// responses from partial-frame cleanup don't corrupt subsequent reads.
// Device-side failures (SPI errors, inflate errors) are NOT retried.
func (f *Flasher) retryFlashBlock(seq, numBlocks uint32, writeFn func() error) error {
	var err error
	for attempt := range flashBlockRetries {
		err = writeFn()
		if err == nil {
			return nil
		}
		if !isRetryableFlashError(err) {
			return err
		}
		if attempt < flashBlockRetries-1 {
			f.logf("Warning: block %d/%d write failed (attempt %d/%d): %v — retrying",
				seq, numBlocks, attempt+1, flashBlockRetries, err)
			// Flush stale data so the retry starts with a clean serial state.
			// After a timeout the stub may still be holding a partial frame;
			// our next send's leading 0xC0 will terminate it, producing a
			// stale error response that the flush on the FOLLOWING iteration
			// (if needed) will clear.
			f.conn.flushInput()
		}
	}
	return err
}

// isRetryableFlashError returns true if the error is a transient serial-link
// issue (data corruption or loss) that can be recovered by resending.
func isRetryableFlashError(err error) bool {
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.IsRetryable()
	}
	var timeoutErr *TimeoutError
	return errors.As(err, &timeoutErr)
}

// connectStatus emits a connect-phase status update if a ConnectStatus
// callback is configured. Purely observational; never affects control flow.
func (f *Flasher) connectStatus(phase ConnectPhase, attempt, maxAttempts int, message string) {
	if f.opts.ConnectStatus != nil {
		f.opts.ConnectStatus(phase, attempt, maxAttempts, message)
	}
}

// FlashID reads the SPI flash chip manufacturer and device ID.
// Returns (manufacturer_id, device_id, error).
func (f *Flasher) FlashID() (uint8, uint16, error) {
	if err := f.attachFlash(); err != nil {
		return 0, 0, err
	}

	// SPIFLASH_RDID command (0x9F) via run_spiflash_command
	// For simplicity, we read it via the SPI registers
	flashID, err := f.runSPIFlashCommand(0x9F, nil, 24)
	if err != nil {
		return 0, 0, err
	}

	// RDID response in W0: byte 0 (LSB) = manufacturer, byte 1 = memory type,
	// byte 2 = capacity. The ESP SPI peripheral stores the first received
	// byte in the lowest bits. This matches esptool.py:
	//   manufacturer = flash_id & 0xFF
	//   capacity     = (flash_id >> 16) & 0xFF
	mfgID := uint8(flashID & 0xFF)
	devID := uint16(((flashID >> 16) & 0xFF) | ((flashID >> 8) & 0xFF << 8))

	return mfgID, devID, nil
}

// MAC returns the factory-programmed base MAC address read from eFuse.
// Works both pre- and post-stub, since ReadRegister uses the READ_REG
// command, which is implemented by both the ROM loader and the stub.
func (f *Flasher) MAC() (net.HardwareAddr, error) {
	if f.chip == nil || f.chip.ReadMAC == nil {
		return nil, &UnsupportedCommandError{Command: "read MAC (chip not detected or unsupported)"}
	}
	return f.chip.ReadMAC(f)
}

// ChipRevision is the eFuse-encoded silicon revision.
type ChipRevision struct {
	Major int
	Minor int
}

// String returns the revision formatted as "vMAJOR.MINOR".
func (r ChipRevision) String() string {
	return fmt.Sprintf("v%d.%d", r.Major, r.Minor)
}

// ChipRevision reads the eFuse-encoded silicon revision. Works both
// pre- and post-stub, like MAC.
func (f *Flasher) ChipRevision() (ChipRevision, error) {
	if f.chip == nil || f.chip.ReadChipRevision == nil {
		return ChipRevision{}, &UnsupportedCommandError{Command: "read chip revision (chip not detected or unsupported)"}
	}
	return f.chip.ReadChipRevision(f)
}

// ChipFeatures returns a human-readable feature list, mirroring esptool's
// get_chip_features(). Works both pre- and post-stub, like MAC.
func (f *Flasher) ChipFeatures() ([]string, error) {
	if f.chip == nil || f.chip.ReadChipFeatures == nil {
		return nil, &UnsupportedCommandError{Command: "read chip features (chip not detected or unsupported)"}
	}
	return f.chip.ReadChipFeatures(f)
}

// decodeEfuseMAC packs two adjacent 32-bit eFuse words into a 6-byte MAC
// address, mirroring esptool's read_mac(): struct.pack(">II", word1, word0)
// trimmed to the middle 6 bytes (the leading 2 bytes of word1 are CRC/other
// bits, not part of the MAC).
func decodeEfuseMAC(word0, word1 uint32) net.HardwareAddr {
	return net.HardwareAddr{
		byte(word1 >> 8),
		byte(word1),
		byte(word0 >> 24),
		byte(word0 >> 16),
		byte(word0 >> 8),
		byte(word0),
	}
}

// runSPIFlashCommand executes a SPI flash command at the register level.
// It configures the SPI peripheral to send 'cmd' as an 8-bit command,
// optionally write 'data' bytes, and read back 'readBits' bits of response.
func (f *Flasher) runSPIFlashCommand(cmd byte, data []byte, readBits int) (uint32, error) {
	if f.chip == nil {
		return 0, fmt.Errorf("chip not detected")
	}

	base := f.chip.SPIRegBase
	spiUSRReg := base + f.chip.SPIUSROffs
	spiUSR1Reg := base + f.chip.SPIUSR1Offs
	spiUSR2Reg := base + f.chip.SPIUSR2Offs
	spiW0Reg := base + f.chip.SPIW0Offs
	spiCMDReg := base // CMD reg is at base

	const (
		spiUSRCommand = 1 << 31
		spiUSRMISO    = 1 << 28
		spiUSRMOSI    = 1 << 27
		spiCMDUSR     = 1 << 18
	)

	// Save old SPI register values
	oldSPIUSR, _ := f.conn.readReg(spiUSRReg)
	oldSPIUSR1, _ := f.conn.readReg(spiUSR1Reg)
	oldSPIUSR2, _ := f.conn.readReg(spiUSR2Reg)

	flags := uint32(spiUSRCommand)
	if readBits > 0 {
		flags |= spiUSRMISO
	}
	if len(data) > 0 {
		flags |= spiUSRMOSI
	}

	f.conn.writeReg(spiUSRReg, flags, 0xFFFFFFFF, 0)                //nolint:errcheck
	f.conn.writeReg(spiUSR2Reg, (7<<28)|uint32(cmd), 0xFFFFFFFF, 0) //nolint:errcheck

	// Configure MISO/MOSI data bit lengths.
	// ESP32-S2+ use dedicated MISO_DLEN / MOSI_DLEN registers.
	// ESP8266 and ESP32 encode lengths in the SPI_USR1 register:
	//   MISO length: bits [7:0] (ESP32) or [4:0] (ESP8266) — value = bitlen-1
	//   MOSI length: bits [25:17] — value = bitlen-1
	if f.chip.SPIMISODLenOffs != 0 {
		// Newer chips: dedicated DLEN registers
		if readBits > 0 {
			f.conn.writeReg(base+f.chip.SPIMISODLenOffs, uint32(readBits-1), 0xFFFFFFFF, 0) //nolint:errcheck
		}
		if len(data) > 0 {
			f.conn.writeReg(base+f.chip.SPIMOSIDLenOffs, uint32(len(data)*8-1), 0xFFFFFFFF, 0) //nolint:errcheck
		}
	} else {
		// ESP8266 / ESP32: set lengths via SPI_USR1 fields
		usr1 := oldSPIUSR1
		if readBits > 0 {
			usr1 = (usr1 &^ 0xFF) | uint32(readBits-1) // bits [7:0] = MISO bitlen-1
		}
		if len(data) > 0 {
			usr1 = (usr1 &^ (0x1FF << 17)) | (uint32(len(data)*8-1) << 17) // bits [25:17] = MOSI bitlen-1
		}
		f.conn.writeReg(spiUSR1Reg, usr1, 0xFFFFFFFF, 0) //nolint:errcheck
	}

	// Write MOSI data to W0 register if present
	if len(data) > 0 {
		var mosiWord uint32
		for i, b := range data {
			mosiWord |= uint32(b) << (8 * uint(i))
		}
		f.conn.writeReg(spiW0Reg, mosiWord, 0xFFFFFFFF, 0) //nolint:errcheck
	}

	// Trigger the SPI user command
	f.conn.writeReg(spiCMDReg, spiCMDUSR, 0xFFFFFFFF, 0) //nolint:errcheck

	// Wait for SPI command to complete
	for i := 0; i < 10; i++ {
		val, _ := f.conn.readReg(spiCMDReg)
		if val&spiCMDUSR == 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}

	result, _ := f.conn.readReg(spiW0Reg)

	// Restore SPI registers
	f.conn.writeReg(spiUSRReg, oldSPIUSR, 0xFFFFFFFF, 0)   //nolint:errcheck
	f.conn.writeReg(spiUSR1Reg, oldSPIUSR1, 0xFFFFFFFF, 0) //nolint:errcheck
	f.conn.writeReg(spiUSR2Reg, oldSPIUSR2, 0xFFFFFFFF, 0) //nolint:errcheck

	return result, nil
}

// flashSizeFromJEDEC converts a JEDEC flash capacity byte to a standard
// size string (e.g. "1MB", "4MB"). The JEDEC capacity byte encodes size
// as 2^N bytes (e.g. 0x14 = 2^20 = 1MB).
// Returns empty string if the capacity byte is out of range or unrecognized.
func flashSizeFromJEDEC(capByte uint8) string {
	if capByte < 18 || capByte > 27 {
		return "" // outside 256KB..128MB range
	}

	sizeBytes := uint64(1) << capByte

	switch {
	case sizeBytes == 256*1024:
		return "256KB"
	case sizeBytes == 512*1024:
		return "512KB"
	case sizeBytes >= 1024*1024:
		return fmt.Sprintf("%dMB", sizeBytes/(1024*1024))
	default:
		return ""
	}
}

// detectFlashSize reads the SPI flash JEDEC ID and determines the flash
// chip capacity. Returns a size string (e.g. "1MB", "4MB") that matches
// the chip's FlashSizes map, or empty string if detection fails.
func (f *Flasher) detectFlashSize() string {
	if f.chip == nil || f.conn == nil {
		return ""
	}

	_, devID, err := f.FlashID()
	if err != nil {
		return ""
	}

	// The lower byte of devID is the JEDEC capacity byte.
	capByte := uint8(devID & 0xFF)
	sizeName := flashSizeFromJEDEC(capByte)
	if sizeName == "" {
		return ""
	}

	// Verify this size is supported by the current chip.
	if _, ok := f.chip.FlashSizes[sizeName]; ok {
		return sizeName
	}

	return ""
}

// StdoutLogger is a simple Logger implementation that writes to an io.Writer.
type StdoutLogger struct {
	W io.Writer
}

// Logf implements the Logger interface.
func (l *StdoutLogger) Logf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(l.W, format+"\n", args...)
}
