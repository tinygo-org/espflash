package espflash

import (
	"bytes"
	"fmt"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	if opts.BaudRate != 115200 {
		t.Errorf("BaudRate = %d, want 115200", opts.BaudRate)
	}
	if opts.FlashBaudRate != 460800 {
		t.Errorf("FlashBaudRate = %d, want 460800", opts.FlashBaudRate)
	}
	if opts.ChipType != ChipAuto {
		t.Errorf("ChipType = %v, want ChipAuto", opts.ChipType)
	}
	if opts.ConnectAttempts != 7 {
		t.Errorf("ConnectAttempts = %d, want 7", opts.ConnectAttempts)
	}
	if !opts.Compress {
		t.Error("Compress should default to true")
	}
	if opts.FlashMode != "" {
		t.Errorf("FlashMode = %q, want empty", opts.FlashMode)
	}
	if opts.FlashFreq != "" {
		t.Errorf("FlashFreq = %q, want empty", opts.FlashFreq)
	}
	if opts.FlashSize != "" {
		t.Errorf("FlashSize = %q, want empty", opts.FlashSize)
	}
	if opts.Logger != nil {
		t.Error("Logger should default to nil")
	}
}

func TestCompressData(t *testing.T) {
	// Compressible data: repeated bytes
	data := bytes.Repeat([]byte{0xAA}, 4096)
	compressed, err := compressData(data)
	if err != nil {
		t.Fatalf("compressData failed: %v", err)
	}
	if len(compressed) >= len(data) {
		t.Errorf("compressed (%d bytes) should be smaller than original (%d bytes)",
			len(compressed), len(data))
	}
	if len(compressed) == 0 {
		t.Error("compressed data should not be empty")
	}
}

func TestCompressDataSmall(t *testing.T) {
	// Very small data may not compress well
	data := []byte{0x01, 0x02, 0x03}
	compressed, err := compressData(data)
	if err != nil {
		t.Fatalf("compressData failed: %v", err)
	}
	// Should still produce valid output even if larger
	if len(compressed) == 0 {
		t.Error("compressed data should not be empty")
	}
}

func TestCompressDataEmpty(t *testing.T) {
	compressed, err := compressData([]byte{})
	if err != nil {
		t.Fatalf("compressData failed: %v", err)
	}
	// Even empty input should produce a valid zlib stream
	if len(compressed) == 0 {
		t.Error("compressed data of empty input should not be empty")
	}
}

func TestLogfNilLogger(t *testing.T) {
	// Ensure logf with nil logger doesn't panic
	f := &Flasher{opts: &FlasherOptions{Logger: nil}}
	f.logf("test %s %d", "hello", 42) // should not panic
}

func TestLogfWithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdoutLogger{W: &buf}
	f := &Flasher{opts: &FlasherOptions{Logger: logger}}
	f.logf("hello %s", "world")

	got := buf.String()
	if got != "hello world\n" {
		t.Errorf("logf output = %q, want %q", got, "hello world\n")
	}
}

func TestStdoutLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := &StdoutLogger{W: &buf}
	logger.Logf("test %d", 42)
	if buf.String() != "test 42\n" {
		t.Errorf("Logf output = %q, want %q", buf.String(), "test 42\n")
	}
}

func TestAttachFlashSkipsESP8266(t *testing.T) {
	// ESP8266 ROM does not support SPI_ATTACH - attachFlash should skip it.
	f := &Flasher{
		chip: defESP8266,
		opts: DefaultOptions(),
	}
	// If it tried to send SPI_ATTACH, it would panic (no port).
	err := f.attachFlash()
	if err != nil {
		t.Errorf("attachFlash for ESP8266 should return nil, got: %v", err)
	}
}

func TestEraseRegionAlignment(t *testing.T) {
	f := &Flasher{
		opts: DefaultOptions(),
		conn: &conn{},
	}

	// Misaligned offset
	err := f.EraseRegion(0x100, 0x1000)
	if err == nil {
		t.Error("expected error for misaligned offset")
	}

	// Misaligned size
	err = f.EraseRegion(0x1000, 0x100)
	if err == nil {
		t.Error("expected error for misaligned size")
	}
}

func TestFlashImageEmptyData(t *testing.T) {
	f := &Flasher{
		opts: DefaultOptions(),
		chip: defESP32,
		conn: &conn{},
	}

	err := f.FlashImage(nil, 0, nil)
	if err == nil {
		t.Error("expected error for nil image data")
	}

	err = f.FlashImage([]byte{}, 0, nil)
	if err == nil {
		t.Error("expected error for empty image data")
	}
}

func TestImagePart(t *testing.T) {
	// Verify ImagePart struct can hold data and offset.
	part := ImagePart{
		Data:   []byte{0xE9, 0x00, 0x02, 0x20},
		Offset: 0x1000,
	}
	if len(part.Data) != 4 {
		t.Errorf("ImagePart.Data len = %d, want 4", len(part.Data))
	}
	if part.Offset != 0x1000 {
		t.Errorf("ImagePart.Offset = 0x%X, want 0x1000", part.Offset)
	}
}

func TestFlasherChipType(t *testing.T) {
	// With chip set
	f := &Flasher{chip: defESP32, opts: DefaultOptions()}
	if f.ChipType() != ChipESP32 {
		t.Errorf("ChipType() = %v, want ChipESP32", f.ChipType())
	}

	// Without chip set
	f2 := &Flasher{opts: DefaultOptions()}
	if f2.ChipType() != ChipAuto {
		t.Errorf("ChipType() = %v, want ChipAuto", f2.ChipType())
	}
}

func TestFlasherChipName(t *testing.T) {
	f := &Flasher{chip: defESP32S3, opts: DefaultOptions()}
	if f.ChipName() != "ESP32-S3" {
		t.Errorf("ChipName() = %q, want %q", f.ChipName(), "ESP32-S3")
	}

	f2 := &Flasher{opts: DefaultOptions()}
	if f2.ChipName() != "Unknown" {
		t.Errorf("ChipName() = %q, want %q", f2.ChipName(), "Unknown")
	}
}

func TestFlashImagePatchesHeader(t *testing.T) {
	// Verify that FlashImage calls patchImageHeader by checking that an
	// invalid flash mode causes an error.
	f := &Flasher{
		opts: &FlasherOptions{
			FlashMode: "invalid_mode",
			FlashSize: "4MB", // set explicitly to skip auto-detection (needs serial port)
			Compress:  false,
		},
		chip: defESP32,
		conn: &conn{},
	}

	data := makeESPImage(FlashModeDIO, 0x20)
	err := f.FlashImage(data, 0, nil)
	if err == nil {
		t.Error("expected error from invalid flash mode during patchImageHeader")
	}
}

func TestFlashSizeFromJEDEC(t *testing.T) {
	tests := []struct {
		capByte  uint8
		expected string
	}{
		{0x12, "256KB"}, // 2^18 = 256KB
		{0x13, "512KB"}, // 2^19 = 512KB
		{0x14, "1MB"},   // 2^20 = 1MB
		{0x15, "2MB"},   // 2^21 = 2MB
		{0x16, "4MB"},   // 2^22 = 4MB
		{0x17, "8MB"},   // 2^23 = 8MB
		{0x18, "16MB"},  // 2^24 = 16MB
		{0x19, "32MB"},  // 2^25 = 32MB
		{0x1A, "64MB"},  // 2^26 = 64MB
		{0x1B, "128MB"}, // 2^27 = 128MB
		{0x11, ""},      // 2^17 = 128KB, below minimum
		{0x1C, ""},      // 2^28 = 256MB, above maximum
		{0x00, ""},      // out of range
		{0xFF, ""},      // out of range
	}

	for _, tt := range tests {
		name := fmt.Sprintf("0x%02X", tt.capByte)
		t.Run(name, func(t *testing.T) {
			got := flashSizeFromJEDEC(tt.capByte)
			if got != tt.expected {
				t.Errorf("flashSizeFromJEDEC(0x%02X) = %q, want %q",
					tt.capByte, got, tt.expected)
			}
		})
	}
}

func TestDetectFlashSizeNilChip(t *testing.T) {
	f := &Flasher{opts: DefaultOptions()}
	got := f.detectFlashSize()
	if got != "" {
		t.Errorf("detectFlashSize() with nil chip = %q, want empty", got)
	}
}

func TestFlashSizeFromJEDECMatchesChipSizes(t *testing.T) {
	// Verify that all JEDEC-detected sizes are present in at least one chip's
	// FlashSizes map.
	jedecSizes := map[uint8]string{
		0x12: "256KB", 0x13: "512KB", 0x14: "1MB", 0x15: "2MB",
		0x16: "4MB", 0x17: "8MB", 0x18: "16MB",
	}

	for capByte, sizeName := range jedecSizes {
		got := flashSizeFromJEDEC(capByte)
		if got != sizeName {
			t.Errorf("flashSizeFromJEDEC(0x%02X) = %q, want %q", capByte, got, sizeName)
		}

		// Check that at least one chip supports this size
		found := false
		for _, def := range chipDefs {
			if _, ok := def.FlashSizes[sizeName]; ok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no chip supports JEDEC-detected size %q (capByte=0x%02X)",
				sizeName, capByte)
		}
	}
}
