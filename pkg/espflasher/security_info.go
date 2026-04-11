package espflasher

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type SecurityInfo struct {
	Flags         uint32
	FlashCryptCnt uint8
	KeyPurposes   [7]uint8
	ChipID        *uint32
	APIVersion    *uint32
	ParsedFlags   ParsedFlags
}

type ParsedFlags struct {
	SecureBootEn               bool
	SecureBootAggressiveRevoke bool
	SecureDownloadEnable       bool
	SecureBootKeyRevoke0       bool
	SecureBootKeyRevoke1       bool
	SecureBootKeyRevoke2       bool
	SoftDisJtag                bool
	HardDisJtag                bool
	DisUSB                     bool
	DisDownloadDcache          bool
	DisDownloadIcache          bool
}

func (f *Flasher) readSecurityInfo() (*SecurityInfo, error) {
	var res []byte
	var err error

	// Use cached security info if available (from ROM before stub was loaded)
	if len(f.secInfo) > 0 {
		res = f.secInfo
	} else {
		// GET_SECURITY_INFO (opcode 0x14) is ROM-only; stub returns 0xC0.
		// If stub is loaded and we have no cached info, it's too late.
		if f.conn.isStub() {
			return nil, &UnsupportedCommandError{
				Command: "GET_SECURITY_INFO requires ROM bootloader; not available after stub is loaded (use ChipAuto to cache during connect)",
			}
		}

		res, err = f.conn.securityInfo()
		if err != nil {
			return nil, err
		}
		// Cache the raw bytes for future calls
		f.secInfo = res
	}

	var si SecurityInfo
	r := bytes.NewReader(res)

	switch len(res) {
	case 20:
		var full struct {
			Flags                          uint32
			B1, B2, B3, B4, B5, B6, B7, B8 uint8
			ChipID                         uint32
			APIVersion                     uint32
		}
		if err := binary.Read(r, binary.LittleEndian, &full); err != nil {
			return nil, fmt.Errorf("parsing security info (20 bytes): %w", err)
		}
		chipID := full.ChipID
		apiVersion := full.APIVersion
		si = SecurityInfo{
			Flags:         full.Flags,
			FlashCryptCnt: full.B1,
			KeyPurposes:   [7]uint8{full.B2, full.B3, full.B4, full.B5, full.B6, full.B7, full.B8},
			ChipID:        &chipID,
			APIVersion:    &apiVersion,
			ParsedFlags:   parseSecurityFlags(full.Flags),
		}
	case 12:
		var short struct {
			Flags                          uint32
			B1, B2, B3, B4, B5, B6, B7, B8 uint8
		}
		if err := binary.Read(r, binary.LittleEndian, &short); err != nil {
			return nil, fmt.Errorf("parsing security info (12 bytes): %w", err)
		}
		si = SecurityInfo{
			Flags:         short.Flags,
			FlashCryptCnt: short.B1,
			KeyPurposes:   [7]uint8{short.B2, short.B3, short.B4, short.B5, short.B6, short.B7, short.B8},
			ParsedFlags:   parseSecurityFlags(short.Flags),
		}
	default:
		return nil, fmt.Errorf("unexpected security info length: %d", len(res))
	}

	return &si, nil
}

func parseSecurityFlags(flags uint32) ParsedFlags {
	return ParsedFlags{
		SecureBootEn:               flags&(1<<0) != 0,
		SecureBootAggressiveRevoke: flags&(1<<1) != 0,
		SecureDownloadEnable:       flags&(1<<2) != 0,
		SecureBootKeyRevoke0:       flags&(1<<3) != 0,
		SecureBootKeyRevoke1:       flags&(1<<4) != 0,
		SecureBootKeyRevoke2:       flags&(1<<5) != 0,
		SoftDisJtag:                flags&(1<<6) != 0,
		HardDisJtag:                flags&(1<<7) != 0,
		DisUSB:                     flags&(1<<8) != 0,
		DisDownloadDcache:          flags&(1<<9) != 0,
		DisDownloadIcache:          flags&(1<<10) != 0,
	}
}
