package espflash

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

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"26m": 0x1,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),
}
