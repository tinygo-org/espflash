package espflasher

import (
	"fmt"
	"strings"
)

// USB VID/PID of the Espressif USB-Serial/JTAG peripheral (ESP32-C3, C5,
// C6, H2, P4, S3), as matched by esptool's uses_usb_jtag_serial().
const (
	espressifUSBVID  = "303A"
	usbSerialJTAGPID = "1001"
)

// samePortName reports whether two port names are the same device. macOS
// exposes one device as both /dev/cu.* and /dev/tty.*.
func samePortName(a, b string) bool {
	return normalizePortName(a) == normalizePortName(b)
}

func normalizePortName(name string) string {
	name = strings.TrimPrefix(name, "/dev/")
	name = strings.TrimPrefix(name, "cu.")
	name = strings.TrimPrefix(name, "tty.")
	return strings.ToLower(name)
}

// usbInterfaceKind identifies the native USB peripheral a serial port
// belongs to, as seen by the host USB stack.
type usbInterfaceKind int

const (
	// usbInterfaceUnknown covers UART bridges, unreported VID/PIDs and
	// ports that couldn't be found.
	usbInterfaceUnknown usbInterfaceKind = iota
	usbInterfaceSerialJTAG
	// usbInterfaceOTG is native USB-OTG (CDC); its PID is the image chip ID.
	usbInterfaceOTG
)

// usbInterfaceFromPort reports which native USB peripheral the port belongs
// to, mirroring esptool's uses_usb_jtag_serial()/uses_usb_otg(). USB-OTG is
// only reported once the chip has been detected, since its PID is the
// chip's image ID.
func (f *Flasher) usbInterfaceFromPort() usbInterfaceKind {
	vid, pid, ok := portVIDPID(f.portStr)
	vid, pid = strings.ToUpper(vid), strings.ToUpper(pid)
	if !ok || vid != espressifUSBVID {
		return usbInterfaceUnknown
	}
	if pid == usbSerialJTAGPID {
		return usbInterfaceSerialJTAG
	}
	if f.chip != nil && pid == fmt.Sprintf("%04X", f.chip.ImageChipID) {
		return usbInterfaceOTG
	}
	return usbInterfaceUnknown
}
