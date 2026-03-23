package espflasher

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

// testFlasher creates a minimal Flasher for testing patchImageHeader.
func testFlasher(opts *FlasherOptions, chip *chipDef) *Flasher {
	if opts == nil {
		opts = DefaultOptions()
	}
	return &Flasher{
		opts: opts,
		chip: chip,
	}
}

// makeESPImage creates a minimal valid ESP image header for testing.
// Returns a 64-byte image (header + padding) with the given mode, sizeFreq byte.
func makeESPImage(mode byte, sizeFreq byte) []byte {
	data := make([]byte, 64)
	data[0] = espImageMagic
	data[1] = 0x00 // segment count
	data[2] = mode
	data[3] = sizeFreq
	return data
}

// makeESPImageWithSHA creates a properly structured ESP image with an appended
// SHA256 hash. The image has 0 segments, so the layout is:
//
//	header(8) + ext_header(16) + padding(7) + checksum(1) = 32 bytes content
//	+ 32 bytes SHA256 = 64 bytes total.
//
// byte 23 bit 0 = 1 indicates SHA is present.
func makeESPImageWithSHA(mode byte, sizeFreq byte) []byte {
	data := make([]byte, 32+32) // 32 bytes content + 32 bytes SHA256
	data[0] = espImageMagic
	data[1] = 0x00 // segment count = 0
	data[2] = mode
	data[3] = sizeFreq
	data[23] = 0x01 // SHA256 flag
	// Bytes 24-30: padding (zero), byte 31: checksum (zero)

	// Compute and append valid SHA256
	content := data[:32]
	hash := sha256.Sum256(content)
	copy(data[32:], hash[:])
	return data
}

func TestPatchImageHeader_NoESPImage(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32)
	data := []byte{0x00, 0x01, 0x02, 0x03}
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if &result[0] != &data[0] {
		t.Error("expected same slice back for non-ESP image")
	}
}

func TestPatchImageHeader_TooShort(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32)
	data := []byte{espImageMagic, 0x00}
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if &result[0] != &data[0] {
		t.Error("expected same slice back for too-short image")
	}
}

func TestPatchImageHeader_KeepMode(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "keep"}, defESP32)
	data := makeESPImage(FlashModeDIO, 0x2F) // dio, 80m, 4MB
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "keep" should return original data unmodified
	if &result[0] != &data[0] {
		t.Error("expected same slice back when keeping all params")
	}
}

func TestPatchImageHeader_NoOptions(t *testing.T) {
	f := testFlasher(&FlasherOptions{}, defESP32)
	data := makeESPImage(FlashModeDIO, 0x2F)
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if &result[0] != &data[0] {
		t.Error("expected same slice back when no options set")
	}
}

func TestPatchImageHeader_FlashMode(t *testing.T) {
	tests := []struct {
		mode     string
		expected byte
	}{
		{"qio", FlashModeQIO},
		{"qout", FlashModeQOUT},
		{"dio", FlashModeDIO},
		{"dout", FlashModeDOUT},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			f := testFlasher(&FlasherOptions{FlashMode: tt.mode}, defESP32)
			data := makeESPImage(0xFF, 0x00)
			result, err := f.patchImageHeader(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result[2] != tt.expected {
				t.Errorf("flash mode byte = 0x%02X, want 0x%02X", result[2], tt.expected)
			}
		})
	}
}

func TestPatchImageHeader_InvalidFlashMode(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "invalid"}, defESP32)
	data := makeESPImage(0x00, 0x00)
	_, err := f.patchImageHeader(data)
	if err == nil {
		t.Fatal("expected error for invalid flash mode")
	}
}

func TestPatchImageHeader_FlashFreq(t *testing.T) {
	tests := []struct {
		freq     string
		expected byte
	}{
		{"80m", 0x0F},
		{"40m", 0x00},
		{"26m", 0x01},
		{"20m", 0x02},
	}

	for _, tt := range tests {
		t.Run(tt.freq, func(t *testing.T) {
			f := testFlasher(&FlasherOptions{FlashFreq: tt.freq}, defESP32)
			data := makeESPImage(0x00, 0xFF) // sizeFreq = 0xFF
			result, err := f.patchImageHeader(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotFreq := result[3] & 0x0F
			if gotFreq != tt.expected {
				t.Errorf("flash freq nibble = 0x%02X, want 0x%02X", gotFreq, tt.expected)
			}
			// Upper nibble should be preserved
			gotSize := result[3] & 0xF0
			if gotSize != 0xF0 {
				t.Errorf("flash size nibble changed: got 0x%02X, want 0xF0", gotSize)
			}
		})
	}
}

func TestPatchImageHeader_FlashSize(t *testing.T) {
	tests := []struct {
		size     string
		expected byte // upper nibble value
	}{
		{"1MB", 0x00},
		{"2MB", 0x10},
		{"4MB", 0x20},
		{"8MB", 0x30},
		{"16MB", 0x40},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			f := testFlasher(&FlasherOptions{FlashSize: tt.size}, defESP32)
			data := makeESPImage(0x00, 0xFF) // sizeFreq = 0xFF
			result, err := f.patchImageHeader(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotSize := result[3] & 0xF0
			if gotSize != tt.expected {
				t.Errorf("flash size nibble = 0x%02X, want 0x%02X", gotSize, tt.expected)
			}
			// Lower nibble should be preserved
			gotFreq := result[3] & 0x0F
			if gotFreq != 0x0F {
				t.Errorf("flash freq nibble changed: got 0x%02X, want 0x0F", gotFreq)
			}
		})
	}
}

func TestPatchImageHeader_FlashFreqNoChip(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashFreq: "40m"}, nil)
	data := makeESPImage(0x00, 0x00)
	_, err := f.patchImageHeader(data)
	if err == nil {
		t.Fatal("expected error when chip is nil and FlashFreq is set")
	}
}

func TestPatchImageHeader_FlashSizeNoChip(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashSize: "4MB"}, nil)
	data := makeESPImage(0x00, 0x00)
	_, err := f.patchImageHeader(data)
	if err == nil {
		t.Fatal("expected error when chip is nil and FlashSize is set")
	}
}

func TestPatchImageHeader_InvalidFlashFreq(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashFreq: "999m"}, defESP32)
	data := makeESPImage(0x00, 0x00)
	_, err := f.patchImageHeader(data)
	if err == nil {
		t.Fatal("expected error for invalid flash frequency")
	}
}

func TestPatchImageHeader_InvalidFlashSize(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashSize: "999MB"}, defESP32)
	data := makeESPImage(0x00, 0x00)
	_, err := f.patchImageHeader(data)
	if err == nil {
		t.Fatal("expected error for invalid flash size")
	}
}

func TestPatchImageHeader_CombineModeFreqSize(t *testing.T) {
	f := testFlasher(&FlasherOptions{
		FlashMode: "dout",
		FlashFreq: "80m",
		FlashSize: "4MB",
	}, defESP32)

	data := makeESPImage(0x00, 0x00) // start with all zeros
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[2] != FlashModeDOUT {
		t.Errorf("mode = 0x%02X, want 0x%02X", result[2], FlashModeDOUT)
	}
	// byte 3: size=0x20 (4MB) | freq=0x0F (80m) = 0x2F
	if result[3] != 0x2F {
		t.Errorf("sizeFreq = 0x%02X, want 0x2F", result[3])
	}
}

func TestPatchImageHeader_DoesNotModifyOriginal(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32)
	data := makeESPImage(FlashModeQIO, 0x00)
	originalByte2 := data[2]
	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Original should not be modified
	if data[2] != originalByte2 {
		t.Error("patchImageHeader modified the original data")
	}
	// Result should be different
	if result[2] != FlashModeDOUT {
		t.Errorf("result mode = 0x%02X, want 0x%02X", result[2], FlashModeDOUT)
	}
}

func TestPatchImageHeader_SHA256Updated(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32)
	data := makeESPImageWithSHA(FlashModeQIO, 0x00)

	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the SHA256 was recomputed
	content := result[:len(result)-32]
	expectedHash := sha256.Sum256(content)
	actualHash := result[len(result)-32:]
	for i := 0; i < 32; i++ {
		if actualHash[i] != expectedHash[i] {
			t.Errorf("SHA256 mismatch at byte %d: got 0x%02X, want 0x%02X",
				i, actualHash[i], expectedHash[i])
		}
	}
}

func TestPatchImageHeader_SHA256NotUpdatedForESP8266(t *testing.T) {
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP8266)
	data := makeESPImageWithSHA(FlashModeQIO, 0x00)
	origSHA := make([]byte, 32)
	copy(origSHA, data[len(data)-32:])

	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ESP8266 should NOT update SHA256 (no extended header)
	actualSHA := result[len(result)-32:]
	for i := 0; i < 32; i++ {
		if actualSHA[i] != origSHA[i] {
			t.Error("SHA256 was updated for ESP8266, but should not have been")
			break
		}
	}
}

func TestPatchImageHeader_ESP8266FlashSizes(t *testing.T) {
	tests := []struct {
		size     string
		expected byte
	}{
		{"512KB", 0x00},
		{"256KB", 0x10},
		{"1MB", 0x20},
		{"2MB", 0x30},
		{"4MB", 0x40},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			f := testFlasher(&FlasherOptions{FlashSize: tt.size}, defESP8266)
			data := makeESPImage(0x00, 0x00)
			result, err := f.patchImageHeader(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotSize := result[3] & 0xF0
			if gotSize != tt.expected {
				t.Errorf("ESP8266 flash size for %s: got 0x%02X, want 0x%02X",
					tt.size, gotSize, tt.expected)
			}
		})
	}
}

func TestFlashModeConstants(t *testing.T) {
	// Verify flash mode constants match the documented values.
	if FlashModeQIO != 0x00 {
		t.Errorf("FlashModeQIO = 0x%02X, want 0x00", FlashModeQIO)
	}
	if FlashModeQOUT != 0x01 {
		t.Errorf("FlashModeQOUT = 0x%02X, want 0x01", FlashModeQOUT)
	}
	if FlashModeDIO != 0x02 {
		t.Errorf("FlashModeDIO = 0x%02X, want 0x02", FlashModeDIO)
	}
	if FlashModeDOUT != 0x03 {
		t.Errorf("FlashModeDOUT = 0x%02X, want 0x03", FlashModeDOUT)
	}
}

func TestFlashModeNames(t *testing.T) {
	expected := map[string]byte{
		"qio":  0x00,
		"qout": 0x01,
		"dio":  0x02,
		"dout": 0x03,
	}
	for name, val := range expected {
		got, ok := flashModeNames[name]
		if !ok {
			t.Errorf("flashModeNames missing %q", name)
			continue
		}
		if got != val {
			t.Errorf("flashModeNames[%q] = 0x%02X, want 0x%02X", name, got, val)
		}
	}
}

// makeESPImageWithSegments creates a properly structured ESP image with the
// given number of segments (each segSize bytes). If withSHA is true, a valid
// SHA256 digest is appended.
func makeESPImageWithSegments(segCount int, segSize int, withSHA bool) []byte {
	// Build image content: header + ext_header + segments + alignment + checksum
	pos := 24 // common header (8) + extended header (16)
	totalSegData := segCount * (8 + segSize)
	contentBeforeChecksum := pos + totalSegData

	// Compute aligned data length: pos + 16 - (pos % 16)
	dataLen := contentBeforeChecksum + 16 - (contentBeforeChecksum % 16)
	totalLen := dataLen
	if withSHA {
		totalLen += 32
	}

	data := make([]byte, totalLen)
	data[0] = espImageMagic
	data[1] = byte(segCount)
	data[2] = FlashModeDIO
	data[3] = 0x20 // 4MB, default freq
	if withSHA {
		data[23] = 0x01
	}

	// Write segment headers
	off := 24
	for i := 0; i < segCount; i++ {
		binary.LittleEndian.PutUint32(data[off:], 0x3F400000+uint32(i*segSize)) // addr
		binary.LittleEndian.PutUint32(data[off+4:], uint32(segSize))            // length
		off += 8
		// Fill segment data with non-zero pattern
		for j := 0; j < segSize; j++ {
			data[off+j] = byte(i + j + 1)
		}
		off += segSize
	}

	// Compute checksum (XOR of all segment data, matching ROM behavior)
	chk := byte(0xEF)
	off = 24
	for i := 0; i < segCount; i++ {
		off += 8 // skip segment header
		for j := 0; j < segSize; j++ {
			chk ^= data[off+j]
		}
		off += segSize
	}
	data[dataLen-1] = chk // checksum is last byte of content

	// Compute and append SHA256 if requested
	if withSHA {
		hash := sha256.Sum256(data[:dataLen])
		copy(data[dataLen:], hash[:])
	}

	return data
}

func TestImageDataLength(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected int
	}{
		{
			name:     "0 segments",
			data:     makeESPImageWithSegments(0, 0, true),
			expected: 32, // 24 + 16 - (24%16) = 32
		},
		{
			name:     "1 segment 16 bytes",
			data:     makeESPImageWithSegments(1, 16, true),
			expected: 64, // 24+8+16=48, 48+16-(48%16)=64
		},
		{
			name:     "2 segments 32 bytes each",
			data:     makeESPImageWithSegments(2, 32, true),
			expected: 112, // 24+2*(8+32)=104, 104+16-(104%16)=112
		},
		{
			name:     "too short",
			data:     make([]byte, 10),
			expected: -1,
		},
		{
			name: "bad magic",
			data: func() []byte {
				d := makeESPImageWithSegments(0, 0, true)
				d[0] = 0x00
				return d
			}(),
			expected: -1,
		},
		{
			name: "truncated segment",
			data: func() []byte {
				// Create image claiming 1 segment but data too short to hold it
				d := make([]byte, 40)
				d[0] = espImageMagic
				d[1] = 1
				d[23] = 0x01
				// Segment at offset 24: addr(4) + len(4)
				binary.LittleEndian.PutUint32(d[24:], 0x3F400000) // addr
				binary.LittleEndian.PutUint32(d[28:], 1000)       // claims 1000 bytes
				return d
			}(),
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := imageDataLength(tt.data)
			if got != tt.expected {
				t.Errorf("imageDataLength() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestPatchImageHeader_SHA256CombinedBinary(t *testing.T) {
	// Simulate a combined binary: bootloader image followed by extra data
	// (partition table + app). The bootloader has append_digest=1.
	bootloader := makeESPImageWithSegments(1, 16, true)
	bootloaderDataLen := imageDataLength(bootloader)

	// Append 256 bytes of "extra data" to simulate partition table + app
	extra := make([]byte, 256)
	for i := range extra {
		extra[i] = byte(0xAA + i)
	}
	combined := append(bootloader, extra...)

	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32C3)
	result, err := f.patchImageHeader(combined)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the bootloader's SHA256 was correctly updated at the right offset
	expectedHash := sha256.Sum256(result[:bootloaderDataLen])
	actualHash := result[bootloaderDataLen : bootloaderDataLen+32]
	for i := 0; i < 32; i++ {
		if actualHash[i] != expectedHash[i] {
			t.Errorf("bootloader SHA256 mismatch at byte %d: got 0x%02X, want 0x%02X",
				i, actualHash[i], expectedHash[i])
		}
	}

	// Verify the trailing data (simulating app) was NOT corrupted
	trailingStart := len(bootloader)
	for i := 0; i < len(extra); i++ {
		if result[trailingStart+i] != extra[i] {
			t.Errorf("trailing data corrupted at offset %d: got 0x%02X, want 0x%02X",
				i, result[trailingStart+i], extra[i])
		}
	}
}

func TestPatchImageHeader_SHA256WithSegments(t *testing.T) {
	// Image with 2 segments and SHA256
	f := testFlasher(&FlasherOptions{FlashMode: "dout"}, defESP32C3)
	data := makeESPImageWithSegments(2, 32, true)

	result, err := f.patchImageHeader(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify SHA is correct
	dataLen := imageDataLength(result)
	if dataLen < 0 {
		t.Fatal("imageDataLength returned -1")
	}

	expectedHash := sha256.Sum256(result[:dataLen])
	actualHash := result[dataLen : dataLen+32]
	for i := 0; i < 32; i++ {
		if actualHash[i] != expectedHash[i] {
			t.Errorf("SHA256 mismatch at byte %d: got 0x%02X, want 0x%02X",
				i, actualHash[i], expectedHash[i])
		}
	}
}
