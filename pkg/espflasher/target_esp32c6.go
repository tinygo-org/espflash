package espflasher

import "net"

// ESP32-C6 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32c6.py
const (
	esp32c6UARTDevBufNo              uint32 = 0x4087F580 // ROM .bss: active console interface
	esp32c6UARTDevBufNoUSBJTAGSerial uint32 = 3          // USB-JTAG/Serial active

	esp32c6LPWDTConfig0     uint32 = 0x600B1C00
	esp32c6LPWDTWProtect    uint32 = 0x600B1C18
	esp32c6LPWDTSWDConf     uint32 = 0x600B1C1C
	esp32c6LPWDTSWDWProtect uint32 = 0x600B1C20

	// EFUSE_BLOCK1 words used for MAC/revision/feature decoding.
	// Reference: esptool/targets/esp32c6.py (EFUSE_BLOCK1_ADDR, MAC_EFUSE_REG,
	// get_major_chip_version/get_minor_chip_version/get_flash_cap).
	esp32c6EfuseBlock1Word0 uint32 = 0x600B0844 // MAC_EFUSE_REG (num_word 0)
	esp32c6EfuseBlock1Word3 uint32 = 0x600B0850 // num_word 3
	esp32c6EfuseBlock1Word4 uint32 = 0x600B0854 // num_word 4
)

// ESP32-C6 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32c6.py

var defESP32C6 = &chipDef{
	ChipType:       ChipESP32C6,
	Name:           "ESP32-C6",
	ImageChipID:    13,
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

	BootloaderFlashOffset: 0x0,

	SupportsEncryptedFlash: true,
	ROMHasCompressedFlash:  true,
	ROMHasChangeBaud:       true,

	FlashFrequency: map[string]byte{
		"80m": 0x0, // workaround for wrong mspi HS div value in ROM
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32c6PostConnect,

	ReadMAC:          esp32c6ReadMAC,
	ReadChipRevision: esp32c6ReadChipRevision,
	ReadChipFeatures: esp32c6ReadChipFeatures,
}

// esp32c6PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the LP WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32c6.py _post_connect()
func esp32c6PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does); fall back to the ROM variable when
	// the host reports none.
	usbJTAG := f.usbInterfaceFromPort() == usbInterfaceSerialJTAG
	if !usbJTAG {
		uartDev, err := f.ReadRegister(esp32c6UARTDevBufNo)
		if err != nil {
			// In secure download mode, the register may be unreadable.
			// Default to non-USB behavior (safe fallback).
			return nil
		}
		usbJTAG = uartDev == esp32c6UARTDevBufNoUSBJTAGSerial
	}

	if usbJTAG {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		return disableWatchdogsLP(f, esp32c6LPWDTConfig0, esp32c6LPWDTWProtect, esp32c6LPWDTSWDConf, esp32c6LPWDTSWDWProtect)
	}

	return nil
}

// esp32c6ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32c6.py read_mac().
func esp32c6ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32c6EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32c6EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32c6ReadChipRevision reads the eFuse-encoded silicon revision.
// Reference: esptool/targets/esp32c6.py get_major_chip_version()/
// get_minor_chip_version().
func esp32c6ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word3, err := f.ReadRegister(esp32c6EfuseBlock1Word3)
	if err != nil {
		return ChipRevision{}, err
	}
	major := (word3 >> 22) & 0x3
	minor := (word3 >> 18) & 0xF
	return ChipRevision{Major: int(major), Minor: int(minor)}, nil
}

// esp32c6ReadChipFeatures returns the chip feature list.
// Reference: esptool/targets/esp32c6.py get_chip_features().
func esp32c6ReadChipFeatures(f *Flasher) ([]string, error) {
	word4, err := f.ReadRegister(esp32c6EfuseBlock1Word4)
	if err != nil {
		return nil, err
	}

	flashCap := word4 & 0x7
	flash, ok := map[uint32]string{
		1: "Embedded Flash 4MB",
		2: "Embedded Flash 8MB",
	}[flashCap]
	if !ok {
		flash = "Unknown Embedded Flash"
	}

	return []string{
		"Wi-Fi 6",
		"BT 5 (LE)",
		"IEEE802.15.4",
		"Single Core + LP Core",
		"160MHz",
		flash,
	}, nil
}
