package espflasher

import "fmt"

// ChipType identifies the ESP chip family.
type ChipType int

const (
	ChipESP8266 ChipType = iota
	ChipESP32
	ChipESP32S2
	ChipESP32S3
	ChipESP32C2
	ChipESP32C3
	ChipESP32C6
	ChipESP32H2
	ChipAuto // Auto-detect chip type
)

// String returns the human-readable chip name.
func (c ChipType) String() string {
	switch c {
	case ChipESP8266:
		return "ESP8266"
	case ChipESP32:
		return "ESP32"
	case ChipESP32S2:
		return "ESP32-S2"
	case ChipESP32S3:
		return "ESP32-S3"
	case ChipESP32C2:
		return "ESP32-C2"
	case ChipESP32C3:
		return "ESP32-C3"
	case ChipESP32C6:
		return "ESP32-C6"
	case ChipESP32H2:
		return "ESP32-H2"
	case ChipAuto:
		return "Auto"
	default:
		return fmt.Sprintf("Unknown(%d)", int(c))
	}
}

// chipDef holds chip-specific constants and register addresses.
// These values are derived from the ESP-IDF and esptool source code.
type chipDef struct {
	// ChipType identifies this chip family.
	ChipType ChipType

	// Name is the display name (e.g. "ESP32-S3").
	Name string

	// MagicValue is read from CHIP_DETECT_MAGIC_REG_ADDR on older chips.
	// Only used for ESP8266 and ESP32 which don't support get_chip_id.
	MagicValue uint32

	// ImageChipID is returned by the GET_SECURITY_INFO command on newer chips.
	// ESP8266 and ESP32 use magic value detection instead (ImageChipID = 0).
	ImageChipID uint32

	// UsesMagicValue indicates this chip uses the magic register for detection
	// rather than the chip ID command.
	UsesMagicValue bool

	// SPI register base and offsets for flash operations.
	SPIRegBase  uint32
	SPIUSROffs  uint32
	SPIUSR1Offs uint32
	SPIUSR2Offs uint32
	SPIMOSIOffs uint32
	SPIMISOOffs uint32
	SPIW0Offs   uint32

	// SecureDownloadMode is true if esptool detects the ROM is in Secure Download Mode
	SecureDownloadMode bool

	// SPIAddrRegMSB: if true, SPI peripheral sends from MSB of 32-bit register.
	SPIAddrRegMSB bool

	// UARTDateReg is used for clock divider reading.
	UARTDateReg uint32
	UARTClkDiv  uint32
	XTALClkDiv  uint32

	// BootloaderFlashOffset is where the bootloader image lives in flash.
	BootloaderFlashOffset uint32

	// SupportsEncryptedFlash indicates that the ROM bootloader supports
	// the encrypted flash parameter (5th param) in flash_begin and
	// flash_defl_begin commands. ESP32-S2 and newer chips support this.
	// ESP8266 and original ESP32 do not.
	SupportsEncryptedFlash bool

	// ROMHasCompressedFlash indicates the ROM bootloader supports the
	// compressed flash commands (FLASH_DEFL_BEGIN/DATA/END, 0x10-0x12).
	// ESP32 and newer ROMs support this; ESP8266 ROM does not.
	ROMHasCompressedFlash bool

	// ROMHasChangeBaud indicates the ROM bootloader supports the
	// CHANGE_BAUD command (0x0F). ESP32+ ROMs support this; ESP8266 does not.
	ROMHasChangeBaud bool

	// SPIMISODLenOffs is the register offset for the MISO data bit length
	// register (relative to SPIRegBase). On ESP32-S2 and newer, MISO/MOSI
	// lengths are in dedicated registers. On ESP8266 and ESP32, these are 0
	// and the lengths are set via fields in the SPI_USR1 register instead.
	SPIMISODLenOffs uint32
	SPIMOSIDLenOffs uint32

	// FlashFrequency maps frequency strings to register values.
	FlashFrequency map[string]byte

	// FlashSizes maps size strings to header byte values.
	FlashSizes map[string]byte
}

// chipDetectMagicRegAddr is the register address that has a different
// value on each chip model. Used for chip auto-detection.
const chipDetectMagicRegAddr uint32 = 0x40001000

// All known chip definitions.
var chipDefs = map[ChipType]*chipDef{
	ChipESP8266: defESP8266,
	ChipESP32:   defESP32,
	ChipESP32S2: defESP32S2,
	ChipESP32S3: defESP32S3,
	ChipESP32C2: defESP32C2,
	ChipESP32C3: defESP32C3,
	ChipESP32C6: defESP32C6,
	ChipESP32H2: defESP32H2,
}

// detectChipByMagic returns the chip definition matching the given magic value,
// or nil if no match is found.
func detectChipByMagic(magic uint32) *chipDef {
	for _, def := range chipDefs {
		if def.UsesMagicValue && def.MagicValue == magic {
			return def
		}
	}
	return nil
}

// defaultFlashSizes returns the standard flash size map for ESP32 and newer chips.
// Values are the upper nibble of image header byte 3 (pre-shifted).
// Byte 3 format: (flash_size << 4) | flash_freq.
func defaultFlashSizes() map[string]byte {
	return map[string]byte{
		"1MB":   0x00,
		"2MB":   0x10,
		"4MB":   0x20,
		"8MB":   0x30,
		"16MB":  0x40,
		"32MB":  0x50,
		"64MB":  0x60,
		"128MB": 0x70,
	}
}
