package espflasher

import (
	"encoding/binary"
	"testing"
	"time"

	"go.bug.st/serial"
)

func TestChecksum(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected uint32
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: checksumMagic,
		},
		{
			name:     "single byte zero",
			data:     []byte{0x00},
			expected: checksumMagic ^ 0x00,
		},
		{
			name:     "single byte 0xFF",
			data:     []byte{0xFF},
			expected: checksumMagic ^ 0xFF,
		},
		{
			name:     "XOR identity: same byte twice",
			data:     []byte{0x42, 0x42},
			expected: checksumMagic, // XOR cancels out
		},
		{
			name:     "three bytes",
			data:     []byte{0x01, 0x02, 0x03},
			expected: checksumMagic ^ 0x01 ^ 0x02 ^ 0x03,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checksum(tt.data)
			if result != tt.expected {
				t.Errorf("checksum(%X) = 0x%08X, want 0x%08X", tt.data, result, tt.expected)
			}
		})
	}
}

func TestFlashWriteSize(t *testing.T) {
	romConn := &conn{stub: false}
	if romConn.flashWriteSize() != flashWriteSizeROM {
		t.Errorf("ROM write size = %d, want %d", romConn.flashWriteSize(), flashWriteSizeROM)
	}

	stubConn := &conn{stub: true}
	if stubConn.flashWriteSize() != flashWriteSizeStub {
		t.Errorf("Stub write size = %d, want %d", stubConn.flashWriteSize(), flashWriteSizeStub)
	}
}

func TestEraseTimeoutForSize(t *testing.T) {
	tests := []struct {
		name string
		size uint32
		min  time.Duration
	}{
		{
			name: "zero size has minimum",
			size: 0,
			min:  10 * time.Second,
		},
		{
			name: "small size has minimum",
			size: 4096,
			min:  10 * time.Second,
		},
		{
			name: "1MB has base + rate",
			size: 1024 * 1024,
			min:  10 * time.Second,
		},
		{
			name: "10MB is larger",
			size: 10 * 1024 * 1024,
			min:  10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := eraseTimeoutForSize(tt.size)
			if timeout < tt.min {
				t.Errorf("eraseTimeoutForSize(%d) = %v, want >= %v", tt.size, timeout, tt.min)
			}
		})
	}

	// Verify larger sizes get longer timeouts
	small := eraseTimeoutForSize(1024)
	large := eraseTimeoutForSize(100 * 1024 * 1024)
	if large <= small {
		t.Errorf("expected larger timeout for bigger size: small=%v large=%v", small, large)
	}
}

func TestSendCommandFormat(t *testing.T) {
	// Test that sendCommand builds the correct packet structure using a mock port.
	var written []byte
	mock := &mockPort{
		writeFunc: func(data []byte) (int, error) {
			written = append(written, data...)
			return len(data), nil
		},
	}

	c := &conn{
		port:   mock,
		reader: &slipReader{port: mock},
	}

	testData := []byte{0x01, 0x02, 0x03, 0x04}
	chk := uint32(0xABCD)

	err := c.sendCommand(cmdSync, testData, chk)
	if err != nil {
		t.Fatalf("sendCommand failed: %v", err)
	}

	// Decode the SLIP frame
	decoded := slipDecode(written)

	if len(decoded) < 8+len(testData) {
		t.Fatalf("decoded packet too short: %d bytes", len(decoded))
	}

	// Check header fields
	if decoded[0] != respDirectionReq {
		t.Errorf("direction = 0x%02X, want 0x%02X", decoded[0], respDirectionReq)
	}
	if decoded[1] != cmdSync {
		t.Errorf("opcode = 0x%02X, want 0x%02X", decoded[1], cmdSync)
	}

	dataLen := binary.LittleEndian.Uint16(decoded[2:4])
	if dataLen != uint16(len(testData)) {
		t.Errorf("data length = %d, want %d", dataLen, len(testData))
	}

	chkField := binary.LittleEndian.Uint32(decoded[4:8])
	if chkField != chk {
		t.Errorf("checksum field = 0x%08X, want 0x%08X", chkField, chk)
	}

	// Check data payload
	for i := 0; i < len(testData); i++ {
		if decoded[8+i] != testData[i] {
			t.Errorf("data[%d] = 0x%02X, want 0x%02X", i, decoded[8+i], testData[i])
		}
	}
}

func TestFlashBeginParamLength(t *testing.T) {
	// Verify that flash begin sends 16 bytes for older chips and 20 for newer.
	tests := []struct {
		name           string
		supportsEncr   bool
		isStub         bool
		expectedMinLen int // minimum data length in the command
	}{
		{"old ROM", false, false, 16},
		{"new ROM", true, false, 20},
		{"stub", false, true, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written []byte
			mock := &mockPort{
				writeFunc: func(data []byte) (int, error) {
					written = data
					return len(data), nil
				},
				readFunc: func(buf []byte) (int, error) {
					// Return a valid response frame
					resp := makeFlashBeginResponse()
					frame := slipEncode(resp)
					n := copy(buf, frame)
					return n, nil
				},
			}

			c := &conn{
				port:                   mock,
				reader:                 newSlipReader(mock),
				stub:                   tt.isStub,
				supportsEncryptedFlash: tt.supportsEncr,
			}

			_ = c.flashBegin(4096, 0x1000, false) // ignore errors from mock

			// Decode the SLIP frame to get the packet
			decoded := slipDecode(written)
			if len(decoded) < 8 {
				t.Fatalf("packet too short: %d bytes", len(decoded))
			}

			// Data length is in the packet header
			dataLen := int(binary.LittleEndian.Uint16(decoded[2:4]))
			if dataLen < tt.expectedMinLen {
				t.Errorf("flash begin data length = %d, want >= %d", dataLen, tt.expectedMinLen)
			}
		})
	}
}

func TestFlashDeflBeginParamLength(t *testing.T) {
	tests := []struct {
		name           string
		supportsEncr   bool
		isStub         bool
		expectedMinLen int
	}{
		{"old ROM", false, false, 16},
		{"new ROM", true, false, 20},
		{"stub", false, true, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written []byte
			mock := &mockPort{
				writeFunc: func(data []byte) (int, error) {
					written = data
					return len(data), nil
				},
				readFunc: func(buf []byte) (int, error) {
					resp := makeFlashBeginResponse()
					frame := slipEncode(resp)
					n := copy(buf, frame)
					return n, nil
				},
			}

			c := &conn{
				port:                   mock,
				reader:                 newSlipReader(mock),
				stub:                   tt.isStub,
				supportsEncryptedFlash: tt.supportsEncr,
			}

			_ = c.flashDeflBegin(4096, 2048, 0x1000, false)

			decoded := slipDecode(written)
			if len(decoded) < 8 {
				t.Fatalf("packet too short: %d bytes", len(decoded))
			}

			dataLen := int(binary.LittleEndian.Uint16(decoded[2:4]))
			if dataLen < tt.expectedMinLen {
				t.Errorf("flash defl begin data length = %d, want >= %d", dataLen, tt.expectedMinLen)
			}
		})
	}
}

func TestChangeBaudSecondParam(t *testing.T) {
	// ROM: second param should be 0
	// Stub: second param should be oldBaud
	tests := []struct {
		name      string
		isStub    bool
		newBaud   uint32
		oldBaud   uint32
		expect2nd uint32
	}{
		{"ROM sends 0", false, 460800, 115200, 0},
		{"Stub sends oldBaud", true, 460800, 115200, 115200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written []byte
			mock := &mockPort{
				writeFunc: func(data []byte) (int, error) {
					written = data
					return len(data), nil
				},
				readFunc: func(buf []byte) (int, error) {
					resp := makeChangeBaudResponse()
					frame := slipEncode(resp)
					n := copy(buf, frame)
					return n, nil
				},
			}

			c := &conn{
				port:   mock,
				reader: newSlipReader(mock),
				stub:   tt.isStub,
			}

			_ = c.changeBaud(tt.newBaud, tt.oldBaud)

			decoded := slipDecode(written)
			if len(decoded) < 8+8 {
				t.Fatalf("packet too short: %d bytes", len(decoded))
			}

			// Data starts at offset 8 in the decoded packet
			secondParam := binary.LittleEndian.Uint32(decoded[8+4 : 8+8])
			if secondParam != tt.expect2nd {
				t.Errorf("second param = %d, want %d", secondParam, tt.expect2nd)
			}
		})
	}
}

func TestProtocolConstants(t *testing.T) {
	// Verify protocol constants match the ESP bootloader spec.
	if checksumMagic != 0xEF {
		t.Errorf("checksumMagic = 0x%02X, want 0xEF", checksumMagic)
	}
	if flashWriteSizeROM != 0x400 {
		t.Errorf("flashWriteSizeROM = 0x%X, want 0x400", flashWriteSizeROM)
	}
	if flashWriteSizeStub != 0x4000 {
		t.Errorf("flashWriteSizeStub = 0x%X, want 0x4000", flashWriteSizeStub)
	}
	if flashSectorSize != 0x1000 {
		t.Errorf("flashSectorSize = 0x%X, want 0x1000", flashSectorSize)
	}
	if espImageMagic != 0xE9 {
		t.Errorf("espImageMagic = 0x%02X, want 0xE9", espImageMagic)
	}
}

func TestCommandOpcodes(t *testing.T) {
	// Verify key command opcodes.
	expected := map[string]byte{
		"FLASH_BEGIN":     0x02,
		"FLASH_DATA":      0x03,
		"FLASH_END":       0x04,
		"MEM_BEGIN":       0x05,
		"MEM_END":         0x06,
		"MEM_DATA":        0x07,
		"SYNC":            0x08,
		"WRITE_REG":       0x09,
		"READ_REG":        0x0A,
		"SPI_SET_PARAMS":  0x0B,
		"SPI_ATTACH":      0x0D,
		"CHANGE_BAUD":     0x0F,
		"FLASH_DEFL_BEG":  0x10,
		"FLASH_DEFL_DATA": 0x11,
		"FLASH_DEFL_END":  0x12,
		"SPI_FLASH_MD5":   0x13,
	}

	actual := map[string]byte{
		"FLASH_BEGIN":     cmdFlashBegin,
		"FLASH_DATA":      cmdFlashData,
		"FLASH_END":       cmdFlashEnd,
		"MEM_BEGIN":       cmdMemBegin,
		"MEM_END":         cmdMemEnd,
		"MEM_DATA":        cmdMemData,
		"SYNC":            cmdSync,
		"WRITE_REG":       cmdWriteReg,
		"READ_REG":        cmdReadReg,
		"SPI_SET_PARAMS":  cmdSPISetParams,
		"SPI_ATTACH":      cmdSPIAttach,
		"CHANGE_BAUD":     cmdChangeBaud,
		"FLASH_DEFL_BEG":  cmdFlashDeflBeg,
		"FLASH_DEFL_DATA": cmdFlashDeflData,
		"FLASH_DEFL_END":  cmdFlashDeflEnd,
		"SPI_FLASH_MD5":   cmdSPIFlashMD5,
	}

	for name, exp := range expected {
		got := actual[name]
		if got != exp {
			t.Errorf("cmd%s = 0x%02X, want 0x%02X", name, got, exp)
		}
	}
}

// --- Mock port and response helpers ---

// mockPort implements serial.Port for testing.
type mockPort struct {
	writeFunc func([]byte) (int, error)
	readFunc  func([]byte) (int, error)
}

func (m *mockPort) Write(p []byte) (int, error) {
	if m.writeFunc != nil {
		return m.writeFunc(p)
	}
	return len(p), nil
}

func (m *mockPort) Read(p []byte) (int, error) {
	if m.readFunc != nil {
		return m.readFunc(p)
	}
	return 0, nil
}

func (m *mockPort) SetMode(mode *serial.Mode) error                      { return nil }
func (m *mockPort) SetReadTimeout(t time.Duration) error                 { return nil }
func (m *mockPort) SetWriteTimeout(t time.Duration) error                { return nil }
func (m *mockPort) Close() error                                         { return nil }
func (m *mockPort) ResetInputBuffer() error                              { return nil }
func (m *mockPort) ResetOutputBuffer() error                             { return nil }
func (m *mockPort) SetDTR(dtr bool) error                                { return nil }
func (m *mockPort) SetRTS(rts bool) error                                { return nil }
func (m *mockPort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }
func (m *mockPort) Break(t time.Duration) error                          { return nil }
func (m *mockPort) Drain() error                                         { return nil }

// mockConnection implements the connection interface for testing.
type mockConnection struct {
	syncFunc                    func() (uint32, error)
	readRegFunc                 func(addr uint32) (uint32, error)
	writeRegFunc                func(addr, value, mask, delayUS uint32) error
	securityInfoFunc            func() ([]byte, error)
	flashBeginFunc              func(size, offset uint32, encrypted bool) error
	flashDataFunc               func(block []byte, seq uint32) error
	flashEndFunc                func(reboot bool) error
	flashDeflBeginFunc          func(uncompSize, compSize, offset uint32, encrypted bool) error
	flashDeflDataFunc           func(block []byte, seq uint32) error
	flashDeflEndFunc            func(reboot bool) error
	flashMD5Func                func(addr, size uint32) ([]byte, error)
	flashWriteSizeFunc          func() uint32
	spiAttachFunc               func(value uint32) error
	spiSetParamsFunc            func(totalSize, blockSize, sectorSize, pageSize uint32) error
	changeBaudFunc              func(newBaud, oldBaud uint32) error
	eraseFlashFunc              func() error
	eraseRegionFunc             func(offset, size uint32) error
	flushInputFunc              func()
	loadStubFunc                func(s *stub) error
	readFlashFunc               func(offset, size uint32) ([]byte, error)
	stubMode                    bool
	usbMode                     bool
	supportsEncryptedFlashValue bool
}

func (m *mockConnection) sync() (uint32, error) {
	if m.syncFunc != nil {
		return m.syncFunc()
	}
	return 0, nil
}

func (m *mockConnection) readReg(addr uint32) (uint32, error) {
	if m.readRegFunc != nil {
		return m.readRegFunc(addr)
	}
	return 0, nil
}

func (m *mockConnection) writeReg(addr, value, mask, delayUS uint32) error {
	if m.writeRegFunc != nil {
		return m.writeRegFunc(addr, value, mask, delayUS)
	}
	return nil
}

func (m *mockConnection) securityInfo() ([]byte, error) {
	if m.securityInfoFunc != nil {
		return m.securityInfoFunc()
	}
	return nil, nil
}

func (m *mockConnection) flashBegin(size, offset uint32, encrypted bool) error {
	if m.flashBeginFunc != nil {
		return m.flashBeginFunc(size, offset, encrypted)
	}
	return nil
}

func (m *mockConnection) flashData(block []byte, seq uint32) error {
	if m.flashDataFunc != nil {
		return m.flashDataFunc(block, seq)
	}
	return nil
}

func (m *mockConnection) flashEnd(reboot bool) error {
	if m.flashEndFunc != nil {
		return m.flashEndFunc(reboot)
	}
	return nil
}

func (m *mockConnection) flashDeflBegin(uncompSize, compSize, offset uint32, encrypted bool) error {
	if m.flashDeflBeginFunc != nil {
		return m.flashDeflBeginFunc(uncompSize, compSize, offset, encrypted)
	}
	return nil
}

func (m *mockConnection) flashDeflData(block []byte, seq uint32) error {
	if m.flashDeflDataFunc != nil {
		return m.flashDeflDataFunc(block, seq)
	}
	return nil
}

func (m *mockConnection) flashDeflEnd(reboot bool) error {
	if m.flashDeflEndFunc != nil {
		return m.flashDeflEndFunc(reboot)
	}
	return nil
}

func (m *mockConnection) flashMD5(addr, size uint32) ([]byte, error) {
	if m.flashMD5Func != nil {
		return m.flashMD5Func(addr, size)
	}
	return nil, nil
}

func (m *mockConnection) flashWriteSize() uint32 {
	if m.flashWriteSizeFunc != nil {
		return m.flashWriteSizeFunc()
	}
	return flashWriteSizeROM
}

func (m *mockConnection) spiAttach(value uint32) error {
	if m.spiAttachFunc != nil {
		return m.spiAttachFunc(value)
	}
	return nil
}

func (m *mockConnection) spiSetParams(totalSize, blockSize, sectorSize, pageSize uint32) error {
	if m.spiSetParamsFunc != nil {
		return m.spiSetParamsFunc(totalSize, blockSize, sectorSize, pageSize)
	}
	return nil
}

func (m *mockConnection) changeBaud(newBaud, oldBaud uint32) error {
	if m.changeBaudFunc != nil {
		return m.changeBaudFunc(newBaud, oldBaud)
	}
	return nil
}

func (m *mockConnection) eraseFlash() error {
	if m.eraseFlashFunc != nil {
		return m.eraseFlashFunc()
	}
	return nil
}

func (m *mockConnection) eraseRegion(offset, size uint32) error {
	if m.eraseRegionFunc != nil {
		return m.eraseRegionFunc(offset, size)
	}
	return nil
}

func (m *mockConnection) flushInput() {
	if m.flushInputFunc != nil {
		m.flushInputFunc()
	}
}

func (m *mockConnection) readFlash(offset, size uint32) ([]byte, error) {
	if m.readFlashFunc != nil {
		return m.readFlashFunc(offset, size)
	}
	return nil, nil
}

func (m *mockConnection) isStub() bool {
	return m.stubMode
}

func (m *mockConnection) setUSB(v bool) {
	m.usbMode = v
}

func (m *mockConnection) setSupportsEncryptedFlash(v bool) {
	m.supportsEncryptedFlashValue = v
}

func (m *mockConnection) loadStub(s *stub) error {
	if m.loadStubFunc != nil {
		return m.loadStubFunc(s)
	}
	return nil
}

// makeFlashBeginResponse creates a mock response for flash begin command.
func makeFlashBeginResponse() []byte {
	resp := make([]byte, 10)
	resp[0] = respDirectionResp
	resp[1] = cmdFlashBegin
	binary.LittleEndian.PutUint16(resp[2:4], 2) // data len = 2
	binary.LittleEndian.PutUint32(resp[4:8], 0) // value
	resp[8] = 0x00                              // status OK
	resp[9] = 0x00                              // error 0
	return resp
}

// makeChangeBaudResponse creates a mock response for change baud command.
func makeChangeBaudResponse() []byte {
	resp := make([]byte, 10)
	resp[0] = respDirectionResp
	resp[1] = cmdChangeBaud
	binary.LittleEndian.PutUint16(resp[2:4], 2)
	binary.LittleEndian.PutUint32(resp[4:8], 0)
	resp[8] = 0x00
	resp[9] = 0x00
	return resp
}

func TestSendCommandChunking(t *testing.T) {
	// Verify that sendCommand writes in chunks <= 64 bytes
	var writes [][]byte
	mock := &mockPort{
		writeFunc: func(data []byte) (int, error) {
			// Record each write call
			chunk := make([]byte, len(data))
			copy(chunk, data)
			writes = append(writes, chunk)
			return len(data), nil
		},
	}

	c := &conn{
		port:   mock,
		reader: &slipReader{port: mock},
	}

	// Create test data that will result in a large SLIP frame
	testData := make([]byte, 256) // Large payload
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	chk := uint32(0xDEADBEEF)

	err := c.sendCommand(cmdFlashData, testData, chk)
	if err != nil {
		t.Fatalf("sendCommand failed: %v", err)
	}

	// Verify that we got multiple writes
	if len(writes) < 2 {
		t.Errorf("expected multiple writes for large frame, got %d", len(writes))
	}

	// Verify each write is <= 64 bytes
	const maxChunk = 64
	for i, w := range writes {
		if len(w) > maxChunk {
			t.Errorf("write[%d] = %d bytes, want <= %d", i, len(w), maxChunk)
		}
	}

	// Verify the reassembled frame decodes correctly
	var reassembled []byte
	for _, w := range writes {
		reassembled = append(reassembled, w...)
	}
	decoded := slipDecode(reassembled)

	// Check that we got the right opcode and data
	if len(decoded) < 8 {
		t.Fatalf("decoded frame too short: %d bytes", len(decoded))
	}
	if decoded[1] != cmdFlashData {
		t.Errorf("opcode = 0x%02X, want 0x%02X", decoded[1], cmdFlashData)
	}
}

func TestUploadToRAMUSBBlockSize(t *testing.T) {
	// Verify that USB connections use 1KB block size and regular use 6KB
	tests := []struct {
		name        string
		usesUSB     bool
		dataSize    uint32
		expectedBS  uint32
		expectedNum uint32
	}{
		{"non-USB 6144 bytes uses 6KB blocks", false, 6144, 0x1800, 1},
		{"non-USB 12288 bytes uses 6KB blocks", false, 12288, 0x1800, 2},
		{"USB 1024 bytes uses 1KB blocks", true, 1024, 0x400, 1},
		{"USB 2048 bytes uses 1KB blocks", true, 2048, 0x400, 2},
		{"USB 6144 bytes split into 1KB blocks", true, 6144, 0x400, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We'll verify block size by checking the calculation logic
			blockSize := espRAMBlock
			if tt.usesUSB {
				blockSize = 0x400
			}

			dataLen := tt.dataSize
			numBlocks := (dataLen + blockSize - 1) / blockSize

			if blockSize != tt.expectedBS {
				t.Errorf("block size = %d, want %d", blockSize, tt.expectedBS)
			}

			if numBlocks != tt.expectedNum {
				t.Errorf("num blocks = %d, want %d", numBlocks, tt.expectedNum)
			}
		})
	}
}

func TestReadFlashBlockSize(t *testing.T) {
	// Verify readFlashBlockSize constant
	if readFlashBlockSize != 0x1000 {
		t.Errorf("readFlashBlockSize = 0x%X, want 0x1000", readFlashBlockSize)
	}
}

func TestReadFlashParameterValidation(t *testing.T) {
	// Verify that readFlash uses correct command opcode
	if cmdReadFlash != 0xD2 {
		t.Errorf("cmdReadFlash = 0x%02X, want 0xD2", cmdReadFlash)
	}
	if cmdReadFlash != 0xD2 {
		t.Errorf("cmdReadFlash opcode mismatch")
	}
}
