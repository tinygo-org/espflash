package espflasher

import "net"

// ESP32-C5 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32c5.py
const (
	esp32c5UARTDevBufNo              uint32 = 0x4085F514 // ROM .bss: active console interface
	esp32c5UARTDevBufNoUSBJTAGSerial uint32 = 3          // USB-JTAG/Serial active

	esp32c5LPWDTConfig0     uint32 = 0x600B1C00
	esp32c5LPWDTWProtect    uint32 = 0x600B1C18
	esp32c5LPWDTSWDConf     uint32 = 0x600B1C1C
	esp32c5LPWDTSWDWProtect uint32 = 0x600B1C20

	// EFUSE_BLOCK1 words used for MAC/revision decoding.
	// Reference: esptool/targets/esp32c5.py (EFUSE_BLOCK1_ADDR, MAC_EFUSE_REG,
	// get_major_chip_version/get_minor_chip_version).
	esp32c5EfuseBlock1Word0 uint32 = 0x600B4844 // MAC_EFUSE_REG (num_word 0)
	esp32c5EfuseBlock1Word2 uint32 = 0x600B484C // num_word 2
)

// ESP32-C5 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32c5.py

var defESP32C5 = &chipDef{
	ChipType:       ChipESP32C5,
	Name:           "ESP32-C5",
	ImageChipID:    23,
	UsesMagicValue: false, // Uses chip ID

	SPIRegBase:  0x60003000,
	SPIUSROffs:  0x18,
	SPIUSR1Offs: 0x1C,
	SPIUSR2Offs: 0x20,
	SPIMOSIOffs: 0x24,
	SPIMISOOffs: 0x98,
	SPIW0Offs:   0x58,

	SPIMISODLenOffs: 0x28,
	SPIMOSIDLenOffs: 0x24,

	SPIAddrRegMSB: true,

	UARTDateReg: 0x60000078,
	UARTClkDiv:  0x60000014,
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

	PostConnect: esp32c5PostConnect,

	ReadMAC:          esp32c5ReadMAC,
	ReadChipRevision: esp32c5ReadChipRevision,
	ReadChipFeatures: esp32c5ReadChipFeatures,
}

// esp32c5PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the LP WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32c5.py _post_connect()
func esp32c5PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does); fall back to the ROM variable when
	// the host reports none.
	usbJTAG := f.usbInterfaceFromPort() == usbInterfaceSerialJTAG
	if !usbJTAG {
		uartDev, err := f.ReadRegister(esp32c5UARTDevBufNo)
		if err != nil {
			// In secure download mode, the register may be unreadable.
			// Default to non-USB behavior (safe fallback).
			return nil
		}
		usbJTAG = uartDev == esp32c5UARTDevBufNoUSBJTAGSerial
	}

	if usbJTAG {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		return disableWatchdogsLP(f, esp32c5LPWDTConfig0, esp32c5LPWDTWProtect, esp32c5LPWDTSWDConf, esp32c5LPWDTSWDWProtect)
	}

	return nil
}

// esp32c5ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32c5.py read_mac().
func esp32c5ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32c5EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32c5EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32c5ReadChipRevision reads the eFuse-encoded silicon revision.
// Reference: esptool/targets/esp32c5.py get_major_chip_version()/
// get_minor_chip_version().
func esp32c5ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word2, err := f.ReadRegister(esp32c5EfuseBlock1Word2)
	if err != nil {
		return ChipRevision{}, err
	}
	major := (word2 >> 4) & 0x3
	minor := word2 & 0xF
	return ChipRevision{Major: int(major), Minor: int(minor)}, nil
}

// esp32c5ReadChipFeatures returns the chip feature list. ESP32-C5 has no
// runtime-detectable flash/PSRAM eFuse bits in esptool, so this is a fixed
// list. Reference: esptool/targets/esp32c5.py get_chip_features().
func esp32c5ReadChipFeatures(f *Flasher) ([]string, error) {
	return []string{
		"Wi-Fi 6 (dual-band)",
		"BT 5 (LE)",
		"IEEE802.15.4",
		"Single Core + LP Core",
		"240MHz",
	}, nil
}
