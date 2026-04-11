package espflasher

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
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
}

// Logger is the interface for receiving progress and status messages.
type Logger interface {
	// Logf logs a formatted informational message.
	Logf(format string, args ...interface{})
}

// ProgressFunc is called with progress updates during flashing.
// current is the bytes transferred so far, total is the total bytes.
type ProgressFunc func(current, total int)

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
	readFlash(offset, size uint32) ([]byte, error)
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

	port, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", portName, err)
	}

	f := &Flasher{
		port:    port,
		conn:    newConn(port),
		opts:    opts,
		portStr: portName,
	}

	// Connect to the bootloader
	if err := f.connect(); err != nil {
		f.port.Close() //nolint:errcheck
		return nil, err
	}

	return f, nil
}

// Close releases the serial port and associated resources.
func (f *Flasher) Close() error {
	return f.port.Close()
}

// reopenPort closes and reopens the serial port after a USB device
// re-enumeration. TinyUSB CDC devices may briefly disappear during reset.
func (f *Flasher) reopenPort() error {
	f.port.Close() //nolint:errcheck

	var lastErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		port, err := serial.Open(f.portStr, &serial.Mode{
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

// connect performs the bootloader connection sequence:
// reset → sync → detect chip.
func (f *Flasher) connect() error {
	f.logf("Connecting to %s...", f.portStr)

	attempts := f.opts.ConnectAttempts
	if attempts <= 0 {
		attempts = 7
	}

	for attempt := 0; attempt < attempts; attempt++ {
		// Reset the chip into bootloader mode
		switch f.opts.ResetMode {
		case ResetDefault:
			if attempt%2 == 0 {
				classicReset(f.port, defaultResetDelay)
			} else {
				tightReset(f.port, defaultResetDelay+500*time.Millisecond)
			}
		case ResetUSBJTAG:
			usbJTAGSerialReset(f.port)
		case ResetAuto:
			switch attempt % 3 {
			case 0:
				classicReset(f.port, defaultResetDelay)
			case 1:
				usbJTAGSerialReset(f.port)
			case 2:
				// No reset — device may already be in bootloader
			}
		}
		// ResetNoReset: skip reset entirely

		// Try to sync with the bootloader
		time.Sleep(100 * time.Millisecond) // Give bootloader time to start
		f.conn.flushInput()
		for range 5 {
			_, err := f.conn.sync()
			if err == nil {
				goto synced
			}
			time.Sleep(50 * time.Millisecond)
		}

		// Sync failed — try reopening port (USB CDC may have re-enumerated)
		if err := f.reopenPort(); err != nil {
			continue // port reopen failed, try next attempt
		}
	}

	return &SyncError{Attempts: attempts}

synced:
	f.logf("Connected.")

	// Detect chip type
	if f.opts.ChipType == ChipAuto {
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
	if s, ok := stubFor(f.chip.ChipType); ok {
		f.logf("Loading stub loader...")
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

		chipDefs[ChipESP32S2].SecureDownloadMode = si.ParsedFlags.SecureDownloadEnable
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

	// Auto-detect flash size when not explicitly set.
	if f.opts.FlashSize == "" || f.opts.FlashSize == "keep" {
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
		if err := f.conn.flashDeflData(block, seq); err != nil {
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
	// For the stub, the end command acts as a write barrier: the stub ACKs each
	// block on receive but writes to flash asynchronously, so the end command
	// ensures the last block is actually written before we proceed.
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

		if err := f.conn.flashData(block, seq); err != nil {
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
func (f *Flasher) EraseFlash() error {
	if !f.conn.isStub() {
		return &UnsupportedCommandError{Command: "erase flash (requires stub)"}
	}

	f.logf("Erasing entire flash...")
	if err := f.conn.eraseFlash(); err != nil {
		return err
	}
	f.logf("Flash erased.")
	return nil
}

// EraseRegion erases a region of flash memory.
// Requires the stub loader to be running.
// Both offset and size must be aligned to the flash sector size (4096 bytes).
func (f *Flasher) EraseRegion(offset, size uint32) error {
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
	return f.conn.eraseRegion(offset, size)
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

// GetMD5 returns the MD5 hash of a flash region.
// Requires the stub loader to be running.
func (f *Flasher) GetMD5(offset, size uint32) (string, error) {
	if !f.conn.isStub() {
		return "", &UnsupportedCommandError{Command: "flash MD5 (requires stub)"}
	}

	if err := f.attachFlash(); err != nil {
		return "", err
	}

	result, err := f.conn.flashMD5(offset, size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(result), nil
}

// ReadFlash reads data from flash memory.
// Requires the stub loader to be running.
func (f *Flasher) ReadFlash(offset, size uint32) ([]byte, error) {
	if !f.conn.isStub() {
		return nil, &UnsupportedCommandError{Command: "read flash (requires stub)"}
	}

	if err := f.attachFlash(); err != nil {
		return nil, err
	}

	return f.conn.readFlash(offset, size)
}

// Reset performs a hard reset of the device, causing it to run user code.
func (f *Flasher) Reset() {
	if f.conn.isStub() {
		// The stub loader needs an explicit flash_begin/flash_end to
		// cleanly exit flash mode before hardware reset.
		f.conn.flashBegin(0, 0, false) //nolint:errcheck
		f.conn.flashEnd(true)          //nolint:errcheck
		time.Sleep(50 * time.Millisecond)
	}

	// For ROM bootloaders, skip flash_begin/flash_end — sending
	// CMD_FLASH_BEGIN after a compressed download may interfere with
	// the flash controller state at offset 0. esptool also just does
	// a hard reset without any flash commands for the ROM path.
	hardReset(f.port, f.usesUSB)
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

	// Configure flash parameters for common 4MB flash
	// These defaults work for most development boards
	err := f.conn.spiSetParams(
		4*1024*1024, // 4MB total
		64*1024,     // 64KB block
		4*1024,      // 4KB sector
		256,         // 256B page
	)
	if err != nil {
		// Don't fail - some ROM versions don't support this
		f.logf("Warning: SPI params config failed (may be OK): %v", err)
	}

	return nil
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
	fmt.Fprintf(l.W, format+"\n", args...)
}
