package espflash

// ESP32-C2 (ESP8684) target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32c2.py

var defESP32C2 = &chipDef{
	ChipType:       ChipESP32C2,
	Name:           "ESP32-C2",
	ImageChipID:    12,
	UsesMagicValue: false, // Uses chip ID

	SPIRegBase:  0x60002000,
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
		"60m": 0xF,
		"30m": 0x0,
		"20m": 0x1,
		"15m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),
}
