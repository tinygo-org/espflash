package espflash

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
	romConn := &conn{isStub: false}
	if romConn.flashWriteSize() != flashWriteSizeROM {
		t.Errorf("ROM write size = %d, want %d", romConn.flashWriteSize(), flashWriteSizeROM)
	}

	stubConn := &conn{isStub: true}
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
				isStub:                 tt.isStub,
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
				isStub:                 tt.isStub,
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
				isStub: tt.isStub,
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
