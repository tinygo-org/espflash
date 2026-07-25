package espflasher

import (
	"fmt"
	"net"
)

// ESP32 (classic) register addresses used for MAC/revision/feature
// decoding. Unlike every later chip, the classic ESP32's efuse layout has
// no BLOCK1; words are read directly off EFUSE_RD_REG_BASE, and the major
// chip revision isn't a bitfield — it's a 3-bit value assembled from two
// efuse bits plus one bit from an entirely separate SYSCON register,
// looked up in a table.
// Reference: esptool/targets/esp32.py (EFUSE_RD_REG_BASE, read_efuse(),
// APB_CTL_DATE_ADDR, get_major_chip_version/get_minor_chip_version/
// get_pkg_version/get_chip_features).
const (
	esp32EfuseWord1 uint32 = 0x3FF5A004 // read_efuse(1)
	esp32EfuseWord2 uint32 = 0x3FF5A008 // read_efuse(2)
	esp32EfuseWord3 uint32 = 0x3FF5A00C // read_efuse(3)
	esp32EfuseWord4 uint32 = 0x3FF5A010 // read_efuse(4)
	esp32EfuseWord5 uint32 = 0x3FF5A014 // read_efuse(5)
	esp32EfuseWord6 uint32 = 0x3FF5A018 // read_efuse(6)

	// APB_CTL_DATE_ADDR = DR_REG_SYSCON_BASE (0x3FF66000) + 0x7C. Bit 31
	// supplies the top bit of the 3-bit major-revision lookup index; it
	// lives outside the eFuse block entirely.
	esp32APBCtlDateReg uint32 = 0x3FF6607C
)

// ESP32 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32.py

var defESP32 = &chipDef{
	ChipType:       ChipESP32,
	Name:           "ESP32",
	MagicValue:     0x00F01D83,
	ImageChipID:    0,
	UsesMagicValue: true,

	SPIRegBase:  0x3FF42000,
	SPIUSROffs:  0x1C,
	SPIUSR1Offs: 0x20,
	SPIUSR2Offs: 0x24,
	SPIMOSIOffs: 0x28,
	SPIMISOOffs: 0x98,
	SPIW0Offs:   0x80,

	SPIAddrRegMSB: true,

	UARTDateReg: 0x60000078,
	UARTClkDiv:  0x3FF40014,
	XTALClkDiv:  1,

	BootloaderFlashOffset: 0x1000,

	ROMHasCompressedFlash: true,
	ROMHasChangeBaud:      true,
	MaxUARTFlashBaud:      230400,

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"26m": 0x1,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	ReadMAC:          esp32ReadMAC,
	ReadChipRevision: esp32ReadChipRevision,
	ReadChipFeatures: esp32ReadChipFeatures,
}

// esp32ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32.py read_mac().
func esp32ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word1, err := f.ReadRegister(esp32EfuseWord1)
	if err != nil {
		return nil, err
	}
	word2, err := f.ReadRegister(esp32EfuseWord2)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word1, word2), nil
}

// esp32MajorChipVersionTable is esptool's lookup table mapping the 3-bit
// combined revision-bit value (from two eFuse bits plus one SYSCON bit) to
// the major chip revision. Combine values not present here (2, 4, 5, 6)
// map to major revision 0.
// Reference: esptool/targets/esp32.py get_major_chip_version().
var esp32MajorChipVersionTable = map[uint32]int{
	0: 0,
	1: 1,
	3: 2,
	7: 3,
}

// esp32ReadChipRevision reads the eFuse-encoded silicon revision. Unlike
// every later chip, the major version isn't a bitfield: it's a lookup-table
// index assembled from two eFuse bits plus one bit read from a SYSCON
// register outside the eFuse block.
// Reference: esptool/targets/esp32.py get_major_chip_version()/
// get_minor_chip_version().
func esp32ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word3, err := f.ReadRegister(esp32EfuseWord3)
	if err != nil {
		return ChipRevision{}, err
	}
	word5, err := f.ReadRegister(esp32EfuseWord5)
	if err != nil {
		return ChipRevision{}, err
	}
	apbCtlDate, err := f.ReadRegister(esp32APBCtlDateReg)
	if err != nil {
		return ChipRevision{}, err
	}

	revBit0 := (word3 >> 15) & 0x1
	revBit1 := (word5 >> 20) & 0x1
	revBit2 := (apbCtlDate >> 31) & 0x1
	combine := (revBit2 << 2) | (revBit1 << 1) | revBit0

	major := esp32MajorChipVersionTable[combine] // default (unlisted) is 0
	minor := (word5 >> 24) & 0x3

	return ChipRevision{Major: major, Minor: int(minor)}, nil
}

// esp32CodingSchemeNames is esptool's literal mapping for the flash
// encoding-coding-scheme feature string.
// Reference: esptool/targets/esp32.py get_chip_features().
var esp32CodingSchemeNames = map[uint32]string{
	0: "None",
	1: "3/4",
	2: "Repeat (UNSUPPORTED)",
	3: "None (may contain encoding data)",
}

// esp32ReadChipFeatures returns the chip feature list.
// Reference: esptool/targets/esp32.py get_chip_features().
func esp32ReadChipFeatures(f *Flasher) ([]string, error) {
	word3, err := f.ReadRegister(esp32EfuseWord3)
	if err != nil {
		return nil, err
	}
	word4, err := f.ReadRegister(esp32EfuseWord4)
	if err != nil {
		return nil, err
	}
	word6, err := f.ReadRegister(esp32EfuseWord6)
	if err != nil {
		return nil, err
	}

	features := []string{"Wi-Fi"}

	if word3&(1<<1) == 0 {
		features = append(features, "BT")
	}

	if word3&(1<<0) != 0 {
		features = append(features, "Single Core + LP Core")
	} else {
		features = append(features, "Dual Core + LP Core")
	}

	if word3&(1<<13) != 0 {
		if word3&(1<<12) != 0 {
			features = append(features, "160MHz")
		} else {
			features = append(features, "240MHz")
		}
	}

	pkgVersion := ((word3 >> 9) & 0x7) | (((word3 >> 2) & 0x1) << 3)
	switch pkgVersion {
	case 2, 4, 5, 6:
		features = append(features, "Embedded Flash")
	}
	if pkgVersion == 6 {
		features = append(features, "Embedded PSRAM")
	}

	if adcVref := (word4 >> 8) & 0x1F; adcVref != 0 {
		features = append(features, "Vref calibration in eFuse")
	}

	if word3>>14&0x1 != 0 {
		features = append(features, "BLK3 partially reserved")
	}

	codingScheme := word6 & 0x3
	features = append(features, fmt.Sprintf("Coding Scheme %s", esp32CodingSchemeNames[codingScheme]))

	return features, nil
}
