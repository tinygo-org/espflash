package espflasher

// ESP32-H2 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32h2.py
const (
	esp32h2UARTDevBufNo              uint32 = 0x4084FEFC // ROM .bss: active console interface
	esp32h2UARTDevBufNoUSBJTAGSerial uint32 = 3          // USB-JTAG/Serial active

	esp32h2LPWDTConfig0      uint32 = 0x600B1C00
	esp32h2LPWDTWProtect     uint32 = 0x600B1C1C // H2 offset differs from C6
	esp32h2LPWDTSWDConf      uint32 = 0x600B1C20
	esp32h2LPWDTSWDWProtect  uint32 = 0x600B1C24
)

// ESP32-H2 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32h2.py

var defESP32H2 = &chipDef{
	ChipType:       ChipESP32H2,
	Name:           "ESP32-H2",
	ImageChipID:    16,
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
		"48m": 0xF,
		"24m": 0x0,
		"16m": 0x1,
		"12m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32h2PostConnect,
}

// esp32h2PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the LP WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32h2.py _post_connect()
func esp32h2PostConnect(f *Flasher) error {
	uartDev, err := f.ReadRegister(esp32h2UARTDevBufNo)
	if err != nil {
		// In secure download mode, the register may be unreadable.
		// Default to non-USB behavior (safe fallback).
		return nil
	}

	if uartDev == esp32h2UARTDevBufNoUSBJTAGSerial {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		return disableWatchdogsLP(f, esp32h2LPWDTConfig0, esp32h2LPWDTWProtect, esp32h2LPWDTSWDConf, esp32h2LPWDTSWDWProtect)
	}

	return nil
}
