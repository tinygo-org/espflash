package espflash

import (
	"crypto/sha256"
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

// makeESPImageWithSHA creates an ESP image with an appended SHA256 hash.
// byte 23 bit 0 = 1 indicates SHA is present.
func makeESPImageWithSHA(mode byte, sizeFreq byte) []byte {
	data := make([]byte, 64+32) // 64 bytes content + 32 bytes SHA256
	data[0] = espImageMagic
	data[1] = 0x00
	data[2] = mode
	data[3] = sizeFreq
	data[23] = 0x01 // SHA256 flag

	// Compute and append valid SHA256
	content := data[:64]
	hash := sha256.Sum256(content)
	copy(data[64:], hash[:])
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
