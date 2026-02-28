package espflash

import (
	"testing"
)

func TestChipTypeString(t *testing.T) {
	tests := []struct {
		chip     ChipType
		expected string
	}{
		{ChipESP8266, "ESP8266"},
		{ChipESP32, "ESP32"},
		{ChipESP32S2, "ESP32-S2"},
		{ChipESP32S3, "ESP32-S3"},
		{ChipESP32C2, "ESP32-C2"},
		{ChipESP32C3, "ESP32-C3"},
		{ChipESP32C6, "ESP32-C6"},
		{ChipESP32H2, "ESP32-H2"},
		{ChipAuto, "Auto"},
		{ChipType(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.chip.String()
			if got != tt.expected {
				t.Errorf("ChipType(%d).String() = %q, want %q", int(tt.chip), got, tt.expected)
			}
		})
	}
}

func TestChipDefsCompleteness(t *testing.T) {
	// Every chip type (except ChipAuto) should have a definition.
	expectedChips := []ChipType{
		ChipESP8266,
		ChipESP32,
		ChipESP32S2,
		ChipESP32S3,
		ChipESP32C2,
		ChipESP32C3,
		ChipESP32C6,
		ChipESP32H2,
	}

	for _, ct := range expectedChips {
		def, ok := chipDefs[ct]
		if !ok {
			t.Errorf("chipDefs missing definition for %s", ct)
			continue
		}
		if def.ChipType != ct {
			t.Errorf("chipDefs[%s].ChipType = %s, want %s", ct, def.ChipType, ct)
		}
		if def.Name == "" {
			t.Errorf("chipDefs[%s].Name is empty", ct)
		}
		if def.FlashSizes == nil || len(def.FlashSizes) == 0 {
			t.Errorf("chipDefs[%s].FlashSizes is empty", ct)
		}
		if def.FlashFrequency == nil || len(def.FlashFrequency) == 0 {
			t.Errorf("chipDefs[%s].FlashFrequency is empty", ct)
		}
	}
}

func TestDetectChipByMagic(t *testing.T) {
	tests := []struct {
		name     string
		magic    uint32
		expected ChipType
		found    bool
	}{
		{"ESP8266", 0xFFF0C101, ChipESP8266, true},
		{"ESP32", 0x00F01D83, ChipESP32, true},
		{"ESP32-S2", 0x000007C6, ChipESP32S2, true},
		{"unknown magic", 0xDEADBEEF, 0, false},
		{"zero magic", 0x00000000, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := detectChipByMagic(tt.magic)
			if tt.found {
				if def == nil {
					t.Fatalf("detectChipByMagic(0x%08X) = nil, want %s", tt.magic, tt.expected)
				}
				if def.ChipType != tt.expected {
					t.Errorf("detectChipByMagic(0x%08X).ChipType = %s, want %s",
						tt.magic, def.ChipType, tt.expected)
				}
			} else {
				if def != nil {
					t.Errorf("detectChipByMagic(0x%08X) = %s, want nil", tt.magic, def.ChipType)
				}
			}
		})
	}
}

func TestDefaultFlashSizes(t *testing.T) {
	sizes := defaultFlashSizes()

	expected := map[string]byte{
		"1MB":   0x00,
		"2MB":   0x10,
		"4MB":   0x20,
		"8MB":   0x30,
		"16MB":  0x40,
		"32MB":  0x50,
		"64MB":  0x60,
		"128MB": 0x70,
	}

	for name, val := range expected {
		got, ok := sizes[name]
		if !ok {
			t.Errorf("defaultFlashSizes() missing %q", name)
			continue
		}
		if got != val {
			t.Errorf("defaultFlashSizes()[%q] = 0x%02X, want 0x%02X", name, got, val)
		}
	}
}

func TestDefaultFlashSizesUpperNibble(t *testing.T) {
	// Flash size values should only occupy the upper nibble (bits 4-7).
	sizes := defaultFlashSizes()
	for name, val := range sizes {
		if val&0x0F != 0 {
			t.Errorf("defaultFlashSizes()[%q] = 0x%02X has non-zero lower nibble", name, val)
		}
	}
}

func TestESP8266FlashSizes(t *testing.T) {
	// ESP8266 uses a different encoding than ESP32+.
	sizes := defESP8266.FlashSizes
	expected := map[string]byte{
		"512KB": 0x00,
		"256KB": 0x10,
		"1MB":   0x20,
		"2MB":   0x30,
		"4MB":   0x40,
	}
	for name, val := range expected {
		got, ok := sizes[name]
		if !ok {
			t.Errorf("ESP8266 FlashSizes missing %q", name)
			continue
		}
		if got != val {
			t.Errorf("ESP8266 FlashSizes[%q] = 0x%02X, want 0x%02X", name, got, val)
		}
	}
}

func TestChipCapabilities(t *testing.T) {
	// ESP8266: no encrypted flash, no ROM compressed flash, no ROM change baud
	if defESP8266.SupportsEncryptedFlash {
		t.Error("ESP8266 should not support encrypted flash")
	}
	if defESP8266.ROMHasCompressedFlash {
		t.Error("ESP8266 should not have ROM compressed flash")
	}
	if defESP8266.ROMHasChangeBaud {
		t.Error("ESP8266 should not have ROM change baud")
	}

	// ESP32: no encrypted flash support, but has ROM compressed flash and change baud
	if defESP32.SupportsEncryptedFlash {
		t.Error("ESP32 should not support encrypted flash")
	}
	if !defESP32.ROMHasCompressedFlash {
		t.Error("ESP32 should have ROM compressed flash")
	}
	if !defESP32.ROMHasChangeBaud {
		t.Error("ESP32 should have ROM change baud")
	}

	// ESP32-S2 and newer: all capabilities
	newerChips := []*chipDef{defESP32S2, defESP32S3, defESP32C2, defESP32C3, defESP32C6, defESP32H2}
	for _, def := range newerChips {
		if !def.SupportsEncryptedFlash {
			t.Errorf("%s should support encrypted flash", def.Name)
		}
		if !def.ROMHasCompressedFlash {
			t.Errorf("%s should have ROM compressed flash", def.Name)
		}
		if !def.ROMHasChangeBaud {
			t.Errorf("%s should have ROM change baud", def.Name)
		}
	}
}

func TestMagicValueChips(t *testing.T) {
	// ESP8266, ESP32, ESP32-S2 use magic value detection
	magicChips := []*chipDef{defESP8266, defESP32, defESP32S2}
	for _, def := range magicChips {
		if !def.UsesMagicValue {
			t.Errorf("%s should use magic value detection", def.Name)
		}
		if def.MagicValue == 0 {
			t.Errorf("%s magic value should not be 0", def.Name)
		}
	}

	// Newer chips don't use magic value detection
	nonMagicChips := []*chipDef{defESP32C3, defESP32C6, defESP32H2}
	for _, def := range nonMagicChips {
		if def.UsesMagicValue {
			t.Errorf("%s should not use magic value detection", def.Name)
		}
	}
}

func TestChipDetectMagicRegAddr(t *testing.T) {
	if chipDetectMagicRegAddr != 0x40001000 {
		t.Errorf("chipDetectMagicRegAddr = 0x%08X, want 0x40001000", chipDetectMagicRegAddr)
	}
}

func TestFlashFrequencyNonEmpty(t *testing.T) {
	// All chips should have at least one flash frequency entry.
	for ct, def := range chipDefs {
		if len(def.FlashFrequency) == 0 {
			t.Errorf("%s (ChipType=%d) has no flash frequencies", def.Name, ct)
		}
	}

	// ESP32-family chips (not C2/H2) should support "40m"
	for _, def := range []*chipDef{defESP8266, defESP32, defESP32S2, defESP32S3, defESP32C3, defESP32C6} {
		if _, ok := def.FlashFrequency["40m"]; !ok {
			t.Errorf("%s missing 40m flash frequency", def.Name)
		}
	}

	// ESP32-C2 uses different freq names
	if _, ok := defESP32C2.FlashFrequency["60m"]; !ok {
		t.Error("ESP32-C2 missing 60m flash frequency")
	}

	// ESP32-H2 uses different freq names
	if _, ok := defESP32H2.FlashFrequency["48m"]; !ok {
		t.Error("ESP32-H2 missing 48m flash frequency")
	}
}

func TestBootloaderFlashOffset(t *testing.T) {
	// ESP8266 and some RISC-V chips use offset 0x0; ESP32/S2/S3 use 0x1000.
	if defESP8266.BootloaderFlashOffset != 0x0 {
		t.Errorf("ESP8266 bootloader offset = 0x%X, want 0x0", defESP8266.BootloaderFlashOffset)
	}
	if defESP32.BootloaderFlashOffset != 0x1000 {
		t.Errorf("ESP32 bootloader offset = 0x%X, want 0x1000", defESP32.BootloaderFlashOffset)
	}
}

func TestSPIDLenRegisters(t *testing.T) {
	// ESP8266 and ESP32 use SPI_USR1 for MISO/MOSI bit length (DLEN offsets = 0).
	for _, def := range []*chipDef{defESP8266, defESP32} {
		if def.SPIMISODLenOffs != 0 {
			t.Errorf("%s SPIMISODLenOffs = 0x%X, want 0 (uses USR1)", def.Name, def.SPIMISODLenOffs)
		}
		if def.SPIMOSIDLenOffs != 0 {
			t.Errorf("%s SPIMOSIDLenOffs = 0x%X, want 0 (uses USR1)", def.Name, def.SPIMOSIDLenOffs)
		}
	}

	// ESP32-S2 and newer use dedicated MISO_DLEN / MOSI_DLEN registers.
	newerChips := []*chipDef{defESP32S2, defESP32S3, defESP32C2, defESP32C3, defESP32C6, defESP32H2}
	for _, def := range newerChips {
		if def.SPIMISODLenOffs != 0x28 {
			t.Errorf("%s SPIMISODLenOffs = 0x%X, want 0x28", def.Name, def.SPIMISODLenOffs)
		}
		if def.SPIMOSIDLenOffs != 0x24 {
			t.Errorf("%s SPIMOSIDLenOffs = 0x%X, want 0x24", def.Name, def.SPIMOSIDLenOffs)
		}
	}
}
