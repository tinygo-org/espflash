package espflasher

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"go.bug.st/serial"
)

// ROM bootloader command opcodes.
// These are shared across all ESP chip families.
const (
	cmdFlashBegin      byte = 0x02 // Start flash download
	cmdFlashData       byte = 0x03 // Flash data block
	cmdFlashEnd        byte = 0x04 // Finish flash download
	cmdMemBegin        byte = 0x05 // Start RAM download
	cmdMemEnd          byte = 0x06 // Finish RAM download / execute
	cmdMemData         byte = 0x07 // RAM data block
	cmdSync            byte = 0x08 // Sync with bootloader
	cmdWriteReg        byte = 0x09 // Write 32-bit memory-mapped register
	cmdReadReg         byte = 0x0A // Read 32-bit memory-mapped register
	cmdSecurityInfoReg byte = 0x14 // Read security info (chip ID, flash encryption, etc.)
	cmdSPISetParams    byte = 0x0B // Configure SPI flash parameters
	cmdSPIAttach       byte = 0x0D // Attach SPI flash
	cmdChangeBaud      byte = 0x0F // Change UART baud rate
	cmdFlashDeflBeg    byte = 0x10 // Start compressed flash download
	cmdFlashDeflData   byte = 0x11 // Compressed flash data block
	cmdFlashDeflEnd    byte = 0x12 // Finish compressed flash download
	cmdSPIFlashMD5     byte = 0x13 // Calculate MD5 of flash region

	// Stub-only commands (available after stub loader is running)
	cmdEraseFlash  byte = 0xD0 // Erase entire flash
	cmdEraseRegion byte = 0xD1 // Erase flash region
	cmdReadFlash   byte = 0xD2 // Read flash contents
	cmdRunUserCode byte = 0xD3 // Run user code

	// Command response indicators
	respDirectionReq  byte = 0x00 // Request direction
	respDirectionResp byte = 0x01 // Response direction
)

// Protocol constants
const (
	// checksumMagic is the initial value for the XOR checksum.
	checksumMagic uint32 = 0xEF

	// espRAMBlock is the maximum block size for RAM writes.
	espRAMBlock uint32 = 0x1800 // 6KB

	// flashWriteSizeROM is the flash write block size when using ROM loader.
	flashWriteSizeROM uint32 = 0x400 // 1KB

	// flashWriteSizeStub is the flash write block size when using stub loader.
	flashWriteSizeStub uint32 = 0x4000 // 16KB

	// flashSectorSize is the minimum flash erase unit.
	flashSectorSize uint32 = 0x1000 // 4KB

	// espImageMagic is the first byte of a valid ESP firmware image.
	espImageMagic byte = 0xE9

	// romInvalidRecvMsg is the error code for invalid message.
	romInvalidRecvMsg byte = 0x05
)

// Default timeouts
const (
	defaultTimeout      = 3 * time.Second
	syncTimeout         = 100 * time.Millisecond
	chipEraseTimeout    = 120 * time.Second
	md5Timeout          = 30 * time.Second
	eraseWritePerMBRate = 10 * time.Second // per megabyte
)

// conn wraps the serial port and provides the low-level protocol operations.
type conn struct {
	port   serial.Port
	reader *slipReader
	stub   bool
	// supportsEncryptedFlash indicates the ROM supports the 5th parameter
	// (encrypted flag) in flash_begin/flash_defl_begin commands.
	// Set based on chip type after detection.
	supportsEncryptedFlash bool
}

// isStub returns whether the stub loader is running.
func (c *conn) isStub() bool {
	return c.stub
}

// setSupportsEncryptedFlash sets whether the ROM supports encrypted flash commands.
func (c *conn) setSupportsEncryptedFlash(v bool) {
	c.supportsEncryptedFlash = v
}

// newConn creates a new protocol connection over the given serial port.
func newConn(port serial.Port) *conn {
	return &conn{
		port:   port,
		reader: newSlipReader(port),
	}
}

// checksum calculates the ESP bootloader checksum (XOR of all bytes).
func checksum(data []byte) uint32 {
	state := checksumMagic
	for _, b := range data {
		state ^= uint32(b)
	}
	return state
}

// sendCommand sends a command packet to the ESP device.
//
// Packet format: [direction=0x00] [opcode] [data_length:16LE] [checksum:32LE] [data...]
func (c *conn) sendCommand(opcode byte, data []byte, chk uint32) error {
	pkt := make([]byte, 8+len(data))
	pkt[0] = respDirectionReq
	pkt[1] = opcode
	binary.LittleEndian.PutUint16(pkt[2:4], uint16(len(data)))
	binary.LittleEndian.PutUint32(pkt[4:8], chk)
	copy(pkt[8:], data)

	frame := slipEncode(pkt)
	_, err := c.port.Write(frame)
	return err
}

// commandResponse represents a parsed response from the ESP device.
type commandResponse struct {
	// Value is the 32-bit value field from the response header.
	Value uint32
	// Data is the response payload (after the 8-byte header).
	Data []byte
}

// command sends a command and reads the response.
// If waitResponse is false, the command is sent but no response is read.
func (c *conn) command(opcode byte, data []byte, chk uint32, timeout time.Duration, waitResponse bool) (*commandResponse, error) {
	if err := c.sendCommand(opcode, data, chk); err != nil {
		return nil, fmt.Errorf("send command 0x%02X: %w", opcode, err)
	}

	if !waitResponse {
		return nil, nil
	}

	// Try to get a matching response (filter for correct opcode)
	for range 100 {
		frame, err := c.reader.ReadFrame(timeout)
		if err != nil {
			return nil, err
		}

		if len(frame) < 8 {
			continue
		}

		resp := frame[0]
		opRet := frame[1]
		val := binary.LittleEndian.Uint32(frame[4:8])
		respData := frame[8:]

		if resp != respDirectionResp {
			continue
		}

		if opRet != opcode {
			// Check if this is an error response to flush
			if len(respData) >= 2 && respData[0] != 0 && respData[1] == romInvalidRecvMsg {
				continue
			}
			continue
		}

		return &commandResponse{
			Value: val,
			Data:  respData,
		}, nil
	}

	return nil, fmt.Errorf("no valid response for command 0x%02X after 100 retries", opcode)
}

// checkCommand sends a command and checks the status in the response.
// Returns the response data on success, or an error if the status is non-zero.
// respDataLen specifies how many bytes of actual response data to expect before the status bytes.
func (c *conn) checkCommand(opDesc string, opcode byte, data []byte, chk uint32, timeout time.Duration, respDataLen int) ([]byte, error) {
	resp, err := c.command(opcode, data, chk, timeout, true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opDesc, err)
	}

	const statusLen = 2 // status bytes are 2 bytes

	if len(resp.Data) < respDataLen+statusLen {
		// Not enough data; check first 2 bytes as status
		if len(resp.Data) >= statusLen {
			if resp.Data[0] != 0 {
				return nil, &CommandError{
					OpCode:  opcode,
					Status:  resp.Data[0],
					ErrCode: resp.Data[1],
				}
			}
		}
		return nil, fmt.Errorf("%s: response too short (%d bytes)", opDesc, len(resp.Data))
	}

	// Status bytes are after the expected response data
	statusOff := respDataLen
	if resp.Data[statusOff] != 0 {
		return nil, &CommandError{
			OpCode:  opcode,
			Status:  resp.Data[statusOff],
			ErrCode: resp.Data[statusOff+1],
		}
	}

	if respDataLen > 0 {
		return resp.Data[:respDataLen], nil
	}

	// Return the 32-bit value from the header when no response data expected
	result := make([]byte, 4)
	binary.LittleEndian.PutUint32(result, resp.Value)
	return result, nil
}

// sync sends the SYNC command to synchronize with the ESP bootloader.
// Returns the response value to detect if a stub flasher is already running.
func (c *conn) sync() (uint32, error) {
	// Sync payload: 0x07 0x07 0x12 0x20 followed by 32 bytes of 0x55
	payload := make([]byte, 36)
	payload[0] = 0x07
	payload[1] = 0x07
	payload[2] = 0x12
	payload[3] = 0x20
	for i := 4; i < 36; i++ {
		payload[i] = 0x55
	}

	resp, err := c.command(cmdSync, payload, 0, syncTimeout, true)
	if err != nil {
		return 0, err
	}

	// Read remaining sync responses (ROM sends up to 7 more)
	for range 7 {
		c.command(0, nil, 0, syncTimeout, true) //nolint:errcheck
	}

	return resp.Value, nil
}

// readReg reads a 32-bit register at the given address.
func (c *conn) readReg(addr uint32) (uint32, error) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, addr)

	result, err := c.checkCommand("read register", cmdReadReg, data, 0, defaultTimeout, 0)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint32(result), nil
}

// writeReg writes a 32-bit value to a register with an optional mask and delay.
func (c *conn) writeReg(addr, value, mask, delayUS uint32) error {
	data := make([]byte, 16)
	binary.LittleEndian.PutUint32(data[0:4], addr)
	binary.LittleEndian.PutUint32(data[4:8], value)
	binary.LittleEndian.PutUint32(data[8:12], mask)
	binary.LittleEndian.PutUint32(data[12:16], delayUS)

	_, err := c.checkCommand("write register", cmdWriteReg, data, 0, defaultTimeout, 0)
	return err
}

// securityInfo reads security-related information from the device.
// Try with 20 bytes first (most chips), fallback to 12 bytes (ESP32-S2).
func (c *conn) securityInfo() ([]byte, error) {
	c.flushInput()
	data := make([]byte, 20)

	result, err := c.checkCommand("get security info", cmdSecurityInfoReg, data, 0, defaultTimeout, 20)
	if err == nil {
		// early return if successful with 20-byte response
		return result, nil
	}

	// fallback to 12-byte response for older ROMs (ESP32-S2)
	data = make([]byte, 12)
	result, err = c.checkCommand("get security info", cmdSecurityInfoReg, data, 0, defaultTimeout, 12)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// memBegin starts a RAM download operation.
func (c *conn) memBegin(size, blocks, blockSize, offset uint32) error {
	data := make([]byte, 16)
	binary.LittleEndian.PutUint32(data[0:4], size)
	binary.LittleEndian.PutUint32(data[4:8], blocks)
	binary.LittleEndian.PutUint32(data[8:12], blockSize)
	binary.LittleEndian.PutUint32(data[12:16], offset)

	_, err := c.checkCommand("begin RAM download", cmdMemBegin, data, 0, defaultTimeout, 0)
	return err
}

// memData sends a block of data for RAM download.
func (c *conn) memData(block []byte, seq uint32) error {
	data := make([]byte, 16+len(block))
	binary.LittleEndian.PutUint32(data[0:4], uint32(len(block)))
	binary.LittleEndian.PutUint32(data[4:8], seq)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	binary.LittleEndian.PutUint32(data[12:16], 0)
	copy(data[16:], block)

	_, err := c.checkCommand("write RAM block", cmdMemData, data, checksum(block), defaultTimeout, 0)
	return err
}

// memEnd finishes RAM download. If execute is true, starts execution at entrypoint.
func (c *conn) memEnd(execute bool, entrypoint uint32) error {
	data := make([]byte, 8)
	if !execute {
		binary.LittleEndian.PutUint32(data[0:4], 1) // 1 = don't execute
	}
	binary.LittleEndian.PutUint32(data[4:8], entrypoint)

	if execute {
		// When executing, ROM may reset before sending response
		return c.sendCommand(cmdMemEnd, data, 0)
	}

	_, err := c.checkCommand("finish RAM download", cmdMemEnd, data, 0, defaultTimeout, 0)
	return err
}

// flashBegin starts a flash download operation. Performs an erase of the target region.
func (c *conn) flashBegin(size, offset uint32, encrypted bool) error {
	writeSize := c.flashWriteSize()
	numBlocks := (size + writeSize - 1) / writeSize

	eraseSize := size
	if !c.stub {
		eraseSize = (numBlocks * writeSize)
	}

	timeout := eraseTimeoutForSize(size)

	// ESP32-S2 and newer ROM bootloaders support a 5th parameter (encrypted
	// flag). ESP8266 and original ESP32 ROM only accept 4 parameters (16 bytes).
	// Sending 20 bytes to those older ROMs causes error 0x05 (invalid message).
	paramLen := 16
	if c.supportsEncryptedFlash || c.stub {
		paramLen = 20
	}
	data := make([]byte, paramLen)
	binary.LittleEndian.PutUint32(data[0:4], eraseSize)
	binary.LittleEndian.PutUint32(data[4:8], numBlocks)
	binary.LittleEndian.PutUint32(data[8:12], writeSize)
	binary.LittleEndian.PutUint32(data[12:16], offset)
	if paramLen == 20 && encrypted {
		binary.LittleEndian.PutUint32(data[16:20], 1)
	}

	_, err := c.checkCommand("begin flash download", cmdFlashBegin, data, 0, timeout, 0)
	return err
}

// flashData sends a block of flash data.
func (c *conn) flashData(block []byte, seq uint32) error {
	data := make([]byte, 16+len(block))
	binary.LittleEndian.PutUint32(data[0:4], uint32(len(block)))
	binary.LittleEndian.PutUint32(data[4:8], seq)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	binary.LittleEndian.PutUint32(data[12:16], 0)
	copy(data[16:], block)

	_, err := c.checkCommand("write flash block", cmdFlashData, data, checksum(block), defaultTimeout, 0)
	return err
}

// flashEnd finishes flash download. If reboot is true, the device reboots.
func (c *conn) flashEnd(reboot bool) error {
	data := make([]byte, 4)
	if !reboot {
		binary.LittleEndian.PutUint32(data, 1) // 1 = don't reboot
	}

	_, err := c.checkCommand("finish flash download", cmdFlashEnd, data, 0, defaultTimeout, 0)
	return err
}

// flashDeflBegin starts a compressed flash download.
func (c *conn) flashDeflBegin(uncompSize, compSize, offset uint32, encrypted bool) error {
	writeSize := c.flashWriteSize()
	numBlocks := (compSize + writeSize - 1) / writeSize

	var writeArg uint32
	if c.stub {
		writeArg = uncompSize // stub handles erase internally
	} else {
		eraseBlocks := (uncompSize + writeSize - 1) / writeSize
		writeArg = eraseBlocks * writeSize
	}

	timeout := eraseTimeoutForSize(uncompSize)

	// ESP32-S2 and newer ROM bootloaders support a 5th parameter (encrypted
	// flag). ESP8266 and original ESP32 ROM only accept 4 parameters (16 bytes).
	paramLen := 16
	if c.supportsEncryptedFlash || c.stub {
		paramLen = 20
	}
	data := make([]byte, paramLen)
	binary.LittleEndian.PutUint32(data[0:4], writeArg)
	binary.LittleEndian.PutUint32(data[4:8], numBlocks)
	binary.LittleEndian.PutUint32(data[8:12], writeSize)
	binary.LittleEndian.PutUint32(data[12:16], offset)
	if paramLen == 20 && encrypted {
		binary.LittleEndian.PutUint32(data[16:20], 1)
	}

	_, err := c.checkCommand("begin compressed flash download", cmdFlashDeflBeg, data, 0, timeout, 0)
	return err
}

// flashDeflData sends a block of compressed flash data.
func (c *conn) flashDeflData(block []byte, seq uint32) error {
	data := make([]byte, 16+len(block))
	binary.LittleEndian.PutUint32(data[0:4], uint32(len(block)))
	binary.LittleEndian.PutUint32(data[4:8], seq)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	binary.LittleEndian.PutUint32(data[12:16], 0)
	copy(data[16:], block)

	_, err := c.checkCommand("write compressed flash block", cmdFlashDeflData, data, checksum(block), defaultTimeout, 0)
	return err
}

// flashDeflEnd finishes compressed flash download.
func (c *conn) flashDeflEnd(reboot bool) error {
	data := make([]byte, 4)
	if !reboot {
		binary.LittleEndian.PutUint32(data, 1)
	}

	_, err := c.checkCommand("finish compressed flash download", cmdFlashDeflEnd, data, 0, defaultTimeout, 0)
	return err
}

// spiAttach attaches the SPI flash.
// value=0 for default internal flash.
// The stub accepts 4 bytes; the ROM bootloader accepts 8 (extra 4 for HSPI config).
func (c *conn) spiAttach(value uint32) error {
	var data []byte
	if c.stub {
		data = make([]byte, 4)
	} else {
		data = make([]byte, 8)
	}
	binary.LittleEndian.PutUint32(data[0:4], value)

	_, err := c.checkCommand("attach SPI flash", cmdSPIAttach, data, 0, defaultTimeout, 0)
	return err
}

// spiSetParams configures the SPI flash chip parameters.
func (c *conn) spiSetParams(totalSize, blockSize, sectorSize, pageSize uint32) error {
	data := make([]byte, 24)
	binary.LittleEndian.PutUint32(data[0:4], 0)            // fl_id = auto
	binary.LittleEndian.PutUint32(data[4:8], totalSize)    // total_size
	binary.LittleEndian.PutUint32(data[8:12], blockSize)   // block_size
	binary.LittleEndian.PutUint32(data[12:16], sectorSize) // sector_size
	binary.LittleEndian.PutUint32(data[16:20], pageSize)   // page_size
	binary.LittleEndian.PutUint32(data[20:24], 0xFFFF)     // status_mask

	_, err := c.checkCommand("set SPI flash params", cmdSPISetParams, data, 0, defaultTimeout, 0)
	return err
}

// flashMD5 reads the MD5 hash of a flash region (stub-only).
func (c *conn) flashMD5(addr, size uint32) ([]byte, error) {
	data := make([]byte, 16)
	binary.LittleEndian.PutUint32(data[0:4], addr)
	binary.LittleEndian.PutUint32(data[4:8], size)
	binary.LittleEndian.PutUint32(data[8:12], 0)
	binary.LittleEndian.PutUint32(data[12:16], 0)

	// MD5 can take a while for large regions
	timeout := md5Timeout
	if size > 1024*1024 {
		timeout = time.Duration(float64(md5Timeout) * float64(size) / float64(1024*1024))
	}

	result, err := c.checkCommand("flash MD5", cmdSPIFlashMD5, data, 0, timeout, 16)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// changeBaud changes the UART baud rate.
func (c *conn) changeBaud(newBaud, oldBaud uint32) error {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], newBaud)
	// ROM bootloader ignores the second parameter; stub uses it to know
	// the current baud rate. Send 0 for ROM to match esptool behavior.
	if c.stub {
		binary.LittleEndian.PutUint32(data[4:8], oldBaud)
	}

	_, err := c.checkCommand("change baud rate", cmdChangeBaud, data, 0, defaultTimeout, 0)
	return err
}

// eraseFlash erases the entire flash chip (stub-only).
func (c *conn) eraseFlash() error {
	_, err := c.checkCommand("erase flash", cmdEraseFlash, nil, 0, chipEraseTimeout, 0)
	return err
}

// eraseRegion erases a region of flash (stub-only).
func (c *conn) eraseRegion(offset, size uint32) error {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], offset)
	binary.LittleEndian.PutUint32(data[4:8], size)

	timeout := eraseTimeoutForSize(size)
	_, err := c.checkCommand("erase region", cmdEraseRegion, data, 0, timeout, 0)
	return err
}

// flashWriteSize returns the appropriate block size based on loader type.
func (c *conn) flashWriteSize() uint32 {
	if c.stub {
		return flashWriteSizeStub
	}
	return flashWriteSizeROM
}

// flushInput discards any unread data from the serial port.
func (c *conn) flushInput() {
	c.port.ResetInputBuffer() //nolint:errcheck
}

// eraseTimeoutForSize calculates an appropriate timeout for erase operations.
func eraseTimeoutForSize(size uint32) time.Duration {
	// Base timeout + per-MB rate
	t := defaultTimeout + time.Duration(float64(eraseWritePerMBRate)*float64(size)/float64(1024*1024))
	if t < 10*time.Second {
		t = 10 * time.Second
	}
	return t
}

// loadStub uploads the stub flasher to RAM and executes it.
// The stub provides extended commands like erase_flash, erase_region, and
// compressed flash writes that are not available in the ROM bootloader.
func (c *conn) loadStub(s *stub) error {
	// Upload the text (code) segment.
	if err := c.uploadToRAM(s.text, s.textStart); err != nil {
		return fmt.Errorf("upload stub text: %w", err)
	}

	// Upload the data segment (if any).
	if len(s.data) > 0 {
		if err := c.uploadToRAM(s.data, s.dataStart); err != nil {
			return fmt.Errorf("upload stub data: %w", err)
		}
	}

	// Execute the stub at its entry point.
	// memEnd sends CMD_MEM_END without waiting for a response since the ROM
	// jumps to the entry point immediately and will not send one.
	if err := c.memEnd(true, s.entry); err != nil {
		return fmt.Errorf("execute stub: %w", err)
	}

	// The stub prints "OHAI" as plain bytes (not SLIP-encoded) when it starts.
	if err := c.waitForOHAI(); err != nil {
		return err
	}

	// Flush any leftover bytes, then reset the SLIP reader so it starts clean.
	c.port.ResetInputBuffer() //nolint:errcheck
	c.reader = newSlipReader(c.port)
	c.stub = true
	return nil
}

// uploadToRAM writes a binary segment to the device's RAM via mem_begin/mem_data.
func (c *conn) uploadToRAM(data []byte, addr uint32) error {
	dataLen := uint32(len(data))
	numBlocks := (dataLen + espRAMBlock - 1) / espRAMBlock

	if err := c.memBegin(dataLen, numBlocks, espRAMBlock, addr); err != nil {
		return err
	}

	seq := uint32(0)
	offset := uint32(0)
	for offset < dataLen {
		end := offset + espRAMBlock
		if end > dataLen {
			end = dataLen
		}
		if err := c.memData(data[offset:end], seq); err != nil {
			return fmt.Errorf("block %d: %w", seq, err)
		}
		offset = end
		seq++
	}
	return nil
}

// waitForOHAI reads raw bytes from the serial port until the stub's "OHAI"
// startup greeting is received, confirming the stub is running.
func (c *conn) waitForOHAI() error {
	const maxBytes = 512
	var buf []byte
	single := make([]byte, 32)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.port.SetReadTimeout(100 * time.Millisecond) //nolint:errcheck
		n, err := c.port.Read(single)
		if n > 0 {
			buf = append(buf, single[:n]...)
			if bytes.Contains(buf, []byte("OHAI")) {
				return nil
			}
			// Avoid unbounded growth; keep only the last maxBytes.
			if len(buf) > maxBytes {
				buf = buf[len(buf)-maxBytes:]
			}
		}
		if err != nil && err != io.EOF {
			continue
		}
	}
	return fmt.Errorf("timeout waiting for stub greeting (OHAI)")
}
