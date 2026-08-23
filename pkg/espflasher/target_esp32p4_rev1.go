package espflasher

import "net"

// ESP32-P4 Rev1 (ECO2, chip revision < 3.0) register addresses for USB
// interface detection and watchdog control.
// Reference: esptool/targets/esp32p4.py (P4-specific values) and
// esptool/targets/esp32.py (ESP32ROM base-class SPI defaults P4 inherits).
//
// UARTDEV_BUF_NO is revision-dependent in esptool:
//
//	rev <  3.0 (ECO2, this target):  0x4FF3FEB0 + 24 = 0x4FF3FEC8
//	rev >= 3.0 (production, future): 0x4FFBFEB0 + 24 = 0x4FFBFEC8
//
// The USB-JTAG/Serial sentinel is 6 on P4 (not 3 like C5/C6/H2).
const (
	esp32p4Rev1UARTDevBufNo          uint32 = 0x4FF3FEC8
	esp32p4UARTDevBufNoUSBJTAGSerial uint32 = 6

	esp32p4LPWDTConfig0     uint32 = 0x50116000
	esp32p4LPWDTWProtect    uint32 = 0x50116018
	esp32p4LPWDTSWDConf     uint32 = 0x5011601C
	esp32p4LPWDTSWDWProtect uint32 = 0x50116020

	// EFUSE_BLOCK1 words used for MAC/revision decoding.
	// Reference: esptool/targets/esp32p4.py (EFUSE_BLOCK1_ADDR, MAC_EFUSE_REG,
	// get_major_chip_version/get_minor_chip_version).
	esp32p4EfuseBlock1Word0 uint32 = 0x5012D044 // MAC_EFUSE_REG (num_word 0)
	esp32p4EfuseBlock1Word2 uint32 = 0x5012D04C // num_word 2
)

// ESP32-P4 Rev1 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32p4.py
// Production silicon (rev >= 3.0) uses a different UARTDEV_BUF_NO address and
// the esp32p4.json stub; it will land as a separate target when we have that hardware.

var defESP32P4Rev1 = &chipDef{
	ChipType:       ChipESP32P4Rev1,
	Name:           "ESP32-P4-Rev1",
	ImageChipID:    18,
	UsesMagicValue: false,

	SPIRegBase:  0x5008D000,
	SPIUSROffs:  0x18,
	SPIUSR1Offs: 0x1C,
	SPIUSR2Offs: 0x20,
	SPIMOSIOffs: 0x24,
	SPIMISOOffs: 0x98,
	SPIW0Offs:   0x58,

	SPIMISODLenOffs: 0x28,
	SPIMOSIDLenOffs: 0x24,

	SPIAddrRegMSB: false,

	UARTDateReg: 0x500CA08C,
	UARTClkDiv:  0x500CA014,
	XTALClkDiv:  1,

	BootloaderFlashOffset: 0x2000,

	SupportsEncryptedFlash: true,
	ROMHasCompressedFlash:  true,
	ROMHasChangeBaud:       true,

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32p4Rev1PostConnect,

	ReadMAC:          esp32p4Rev1ReadMAC,
	ReadChipRevision: esp32p4Rev1ReadChipRevision,
	ReadChipFeatures: esp32p4Rev1ReadChipFeatures,
}

func esp32p4Rev1PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does); fall back to the ROM variable when
	// the host reports none.
	usbJTAG := f.usbInterfaceFromPort() == usbInterfaceSerialJTAG
	if !usbJTAG {
		uartDev, err := f.ReadRegister(esp32p4Rev1UARTDevBufNo)
		if err != nil {
			return nil
		}
		usbJTAG = uartDev == esp32p4UARTDevBufNoUSBJTAGSerial
	}

	if usbJTAG {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected (ESP32-P4 rev1), disabling watchdogs")
		return disableWatchdogsLP(f, esp32p4LPWDTConfig0, esp32p4LPWDTWProtect, esp32p4LPWDTSWDConf, esp32p4LPWDTSWDWProtect)
	}

	return nil
}

// esp32p4Rev1ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32p4.py read_mac().
func esp32p4Rev1ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32p4EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32p4EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32p4Rev1ReadChipRevision reads the eFuse-encoded silicon revision.
// The major version is split: bit 2 comes from eFuse bit 23, bits 1:0 come
// from eFuse bits 5:4.
// Reference: esptool/targets/esp32p4.py get_major_chip_version()/
// get_minor_chip_version().
func esp32p4Rev1ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word2, err := f.ReadRegister(esp32p4EfuseBlock1Word2)
	if err != nil {
		return ChipRevision{}, err
	}
	major := (((word2 >> 23) & 0x1) << 2) | ((word2 >> 4) & 0x3)
	minor := word2 & 0xF
	return ChipRevision{Major: int(major), Minor: int(minor)}, nil
}

// esp32p4Rev1ReadChipFeatures returns the chip feature list. ESP32-P4 has
// no runtime-detectable flash/PSRAM eFuse bits in esptool, so this is a
// fixed list. Reference: esptool/targets/esp32p4.py get_chip_features().
func esp32p4Rev1ReadChipFeatures(f *Flasher) ([]string, error) {
	return []string{"Dual Core + LP Core", "400MHz"}, nil
}
