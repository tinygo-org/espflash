package espflash

// ESP32-S3 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32s3.py

var defESP32S3 = &chipDef{
	ChipType:       ChipESP32S3,
	Name:           "ESP32-S3",
	ImageChipID:    9,
	UsesMagicValue: false, // Uses chip ID, not magic value

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
		"80m": 0xF,
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),
}
