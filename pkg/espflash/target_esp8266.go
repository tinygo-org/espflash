package espflash

// ESP8266 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp8266.py

var defESP8266 = &chipDef{
	ChipType:       ChipESP8266,
	Name:           "ESP8266",
	MagicValue:     0xFFF0C101,
	UsesMagicValue: true,

	SPIRegBase:  0x60000200,
	SPIUSROffs:  0x1C,
	SPIUSR1Offs: 0x20,
	SPIUSR2Offs: 0x24,
	SPIMOSIOffs: 0x28,
	SPIMISOOffs: 0x98, // SPI_W0 for read
	SPIW0Offs:   0x40,

	SPIAddrRegMSB: true,

	UARTDateReg: 0x60000078,
	UARTClkDiv:  0x60000014,
	XTALClkDiv:  2, // ESP8266 has 2x divider

	BootloaderFlashOffset: 0x0,

	FlashFrequency: map[string]byte{
		"40m": 0x0,
		"26m": 0x1,
		"20m": 0x2,
		"80m": 0xF,
	},

	// ESP8266 uses a different flash size encoding than ESP32+.
	FlashSizes: map[string]byte{
		"256KB":  0x10,
		"512KB":  0x00,
		"1MB":    0x20,
		"2MB":    0x30,
		"4MB":    0x40,
		"2MB-c1": 0x50,
		"4MB-c1": 0x60,
		"4MB-c2": 0x70,
		"8MB":    0x80,
		"16MB":   0x90,
	},
}
