package espflash

import (
	"crypto/sha256"
	"fmt"
)

// Flash mode constants for the ESP image header (byte offset 2).
// These control how the SPI flash chip is accessed.
const (
	FlashModeQIO  byte = 0x00 // Quad I/O (fastest, 4-bit addr + 4-bit data)
	FlashModeQOUT byte = 0x01 // Quad Output (4-bit data only)
	FlashModeDIO  byte = 0x02 // Dual I/O (2-bit addr + 2-bit data)
	FlashModeDOUT byte = 0x03 // Dual Output (2-bit data, most compatible)
)

// flashModeNames maps user-facing flash mode strings to their byte values.
var flashModeNames = map[string]byte{
	"qio":  FlashModeQIO,
	"qout": FlashModeQOUT,
	"dio":  FlashModeDIO,
	"dout": FlashModeDOUT,
}

// patchImageHeader patches the flash parameters in an ESP firmware image header.
//
// The ESP image header stores flash configuration in the first 4 bytes:
//   - Byte 0: Magic (0xE9)
//   - Byte 2: Flash mode (QIO/QOUT/DIO/DOUT)
//   - Byte 3: Flash size (upper nibble) | Flash frequency (lower nibble)
//
// This function patches bytes 2 and 3 based on FlashMode, FlashFreq, and
// FlashSize in the flasher options. If the image has an appended SHA256 hash
// (indicated by bit 0 of byte 23 in the extended header), the hash is
// recomputed after patching.
//
// Returns a new copy of the data if modifications were made, or the original
// data if no patching was needed.
func (f *Flasher) patchImageHeader(data []byte) ([]byte, error) {
	if len(data) < 4 || data[0] != espImageMagic {
		return data, nil // Not an ESP image, leave as-is
	}

	// Determine what needs patching (0xFF = keep existing value)
	var (
		newFlashMode byte = 0xFF
		newFlashFreq byte = 0xFF
		newFlashSize byte = 0xFF
	)

	if f.opts.FlashMode != "" && f.opts.FlashMode != "keep" {
		fm, ok := flashModeNames[f.opts.FlashMode]
		if !ok {
			return nil, fmt.Errorf("unknown flash mode %q (valid: qio, qout, dio, dout)", f.opts.FlashMode)
		}
		newFlashMode = fm
	}

	if f.opts.FlashFreq != "" && f.opts.FlashFreq != "keep" {
		if f.chip == nil {
			return nil, fmt.Errorf("chip not detected, cannot set flash frequency")
		}
		ff, ok := f.chip.FlashFrequency[f.opts.FlashFreq]
		if !ok {
			return nil, fmt.Errorf("unknown flash frequency %q for %s", f.opts.FlashFreq, f.chip.Name)
		}
		newFlashFreq = ff
	}

	if f.opts.FlashSize != "" && f.opts.FlashSize != "keep" {
		if f.chip == nil {
			return nil, fmt.Errorf("chip not detected, cannot set flash size")
		}
		fs, ok := f.chip.FlashSizes[f.opts.FlashSize]
		if !ok {
			return nil, fmt.Errorf("unknown flash size %q for %s", f.opts.FlashSize, f.chip.Name)
		}
		newFlashSize = fs
	}

	// If nothing needs changing, return original data
	if newFlashMode == 0xFF && newFlashFreq == 0xFF && newFlashSize == 0xFF {
		return data, nil
	}

	// Make a copy to avoid modifying the caller's slice
	patched := make([]byte, len(data))
	copy(patched, data)

	// Patch byte 2: flash mode
	if newFlashMode != 0xFF {
		patched[2] = newFlashMode
	}

	// Patch byte 3: flash size (upper nibble) | flash frequency (lower nibble)
	if newFlashFreq != 0xFF || newFlashSize != 0xFF {
		b3 := patched[3]
		if newFlashSize != 0xFF {
			b3 = (b3 & 0x0F) | newFlashSize // replace upper nibble (size is pre-shifted)
		}
		if newFlashFreq != 0xFF {
			b3 = (b3 & 0xF0) | newFlashFreq // replace lower nibble
		}
		patched[3] = b3
	}

	f.logf("Flash params set to 0x%02X%02X", patched[2], patched[3])

	// Update SHA256 hash if present.
	// The extended header is at bytes 8-23, and byte 23 bit 0 indicates SHA256
	// is appended as the last 32 bytes. ESP8266 doesn't have an extended header,
	// so skip SHA256 update for it.
	if f.chip != nil && f.chip.ChipType != ChipESP8266 &&
		len(patched) >= 24+32 && patched[23]&1 != 0 {
		content := patched[:len(patched)-32]
		hash := sha256.Sum256(content)
		copy(patched[len(patched)-32:], hash[:])
		f.logf("SHA digest in image updated")
	}

	return patched, nil
}
