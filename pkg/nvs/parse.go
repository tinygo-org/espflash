package nvs

import (
	"encoding/binary"
	"fmt"
)

// ParseNVS reads an NVS partition binary and returns a flat slice of entries.
// It handles:
// - Page validation (state, version, header CRC)
// - Entry bitmap decoding
// - Namespace mapping
// - Multi-span entries (strings and blobs)
// - Value decoding based on type
// - Deduplication (last write wins)
func ParseNVS(data []byte) ([]Entry, error) {
	// Validate data length is a multiple of PageSize
	if len(data)%PageSize != 0 {
		return nil, fmt.Errorf("data length (%d) must be a multiple of PageSize (%d)", len(data), PageSize)
	}

	// Map from namespace index to namespace name
	namespaceMap := make(map[uint8]string)

	// Map from "namespace:key" to entry, for deduplication (last write wins)
	entryMap := make(map[string]*Entry)

	// Walk pages
	totalPages := len(data) / PageSize
	for pageNum := 0; pageNum < totalPages; pageNum++ {
		pageOffset := pageNum * PageSize
		page := data[pageOffset : pageOffset+PageSize]

		// Check page state
		state := page[0]
		if state == pageStateEmpty {
			// Skip empty pages
			continue
		}

		// Validate page version at byte 8
		if page[8] != pageVersion {
			// Skip pages with wrong version
			continue
		}

		// Validate header CRC32: bytes 4-27, result at bytes 28-31
		expectedCRC := binary.LittleEndian.Uint32(page[28:32])
		actualCRC := espCRC32(page[4:28])
		if actualCRC != expectedCRC {
			return nil, fmt.Errorf("page %d header CRC mismatch: expected 0x%x, got 0x%x", pageNum, expectedCRC, actualCRC)
		}

		// Read entry bitmap at HeaderSize (bytes 32-63)
		// Walk through bitmap, looking for entries with state = entryStateWritten (0b10)
		processedSlots := make(map[int]bool) // Track multi-span entries

		for slotIdx := 0; slotIdx < EntriesPerPage; slotIdx++ {
			// Skip if already processed as part of multi-span
			if processedSlots[slotIdx] {
				continue
			}

			// Read 2-bit state from bitmap
			bitIndex := uint(slotIdx) * 2
			byteIdx := HeaderSize + int(bitIndex/8)
			bitOffset := bitIndex % 8
			entryState := (page[byteIdx] >> bitOffset) & 0x3

			// Only process entries with state = entryStateWritten (0b10)
			if entryState != entryStateWritten {
				continue
			}

			// Read entry at FirstEntryOffset + slotIdx * EntrySize
			entryOffset := FirstEntryOffset + slotIdx*EntrySize
			if entryOffset+EntrySize > len(page) {
				continue
			}

			entryBytes := page[entryOffset : entryOffset+EntrySize]
			namespaceIdx := entryBytes[0]
			entryType := entryBytes[1]
			span := entryBytes[2]
			// chunkIndex := entryBytes[3] // Not used for parsing
			// crc32Val := binary.LittleEndian.Uint32(entryBytes[4:8]) // TODO: validate CRC
			key := readNullTerminatedString(entryBytes[8:24])
			dataBytes := entryBytes[24:32]

			// Mark all slots used by this entry as processed
			for s := 0; s < int(span); s++ {
				processedSlots[slotIdx+s] = true
			}

			// Handle namespace entries (namespaceIdx == 0)
			if namespaceIdx == 0 && entryType == namespaceType {
				nsIndex := dataBytes[0]
				namespaceMap[nsIndex] = key
				continue
			}

			// Look up namespace name
			namespaceName, ok := namespaceMap[namespaceIdx]
			if !ok {
				// Namespace not yet defined, skip for now
				continue
			}

			// Decode value based on type
			var value interface{}
			var err error

			switch entryType {
			case typeU8:
				value = dataBytes[0]

			case typeU16:
				value = binary.LittleEndian.Uint16(dataBytes[0:2])

			case typeU32:
				value = binary.LittleEndian.Uint32(dataBytes[0:4])

			case typeI8:
				value = int8(dataBytes[0])

			case typeI16:
				value = int16(binary.LittleEndian.Uint16(dataBytes[0:2]))

			case typeI32:
				value = int32(binary.LittleEndian.Uint32(dataBytes[0:4]))

			case typeString:
				value, err = readStringEntry(page, pageNum, slotIdx, dataBytes, span)
				if err != nil {
					return nil, err
				}

			case typeBlob:
				value, err = readBlobEntry(page, pageNum, slotIdx, dataBytes, span)
				if err != nil {
					return nil, err
				}

			default:
				// Skip unknown types
				continue
			}

			// Create entry and store in map (deduplication: last write wins)
			mapKey := fmt.Sprintf("%s:%s", namespaceName, key)
			entryMap[mapKey] = &Entry{
				Namespace: namespaceName,
				Key:       key,
				Type:      typeToString(entryType),
				Value:     value,
			}
		}
	}

	// Convert map to flat slice
	var result []Entry
	for _, e := range entryMap {
		result = append(result, *e)
	}

	return result, nil
}

// readNullTerminatedString reads a null-terminated string from a byte array
func readNullTerminatedString(b []byte) string {
	for i, by := range b {
		if by == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// readStringEntry reads a string entry from data and subsequent span entries
func readStringEntry(page []byte, pageNum int, slotIdx int, headerData []byte, span uint8) (string, error) {
	strLen := binary.LittleEndian.Uint16(headerData[0:2])
	if strLen == 0 {
		return "", nil
	}

	// Read data from subsequent span slots
	var buf []byte
	currentSlot := slotIdx + 1

	for i := 0; i < int(span)-1; i++ {
		if currentSlot >= EntriesPerPage {
			// Would need to read from next page, but for simplicity assume fits in current page
			// In a full implementation, would handle page boundaries
			break
		}

		dataOffset := FirstEntryOffset + currentSlot*EntrySize
		if dataOffset+EntrySize > len(page) {
			break
		}

		buf = append(buf, page[dataOffset:dataOffset+EntrySize]...)
		currentSlot++
	}

	// Trim to actual length and remove null terminator
	if int(strLen) > len(buf) {
		strLen = uint16(len(buf))
	}
	result := buf[:strLen]
	if len(result) > 0 && result[len(result)-1] == 0 {
		result = result[:len(result)-1]
	}
	return string(result), nil
}

// readBlobEntry reads a blob entry from data and subsequent span entries
func readBlobEntry(page []byte, pageNum int, slotIdx int, headerData []byte, span uint8) ([]byte, error) {
	blobLen := binary.LittleEndian.Uint16(headerData[0:2])
	if blobLen == 0 {
		return []byte{}, nil
	}

	// Read data from subsequent span slots
	var buf []byte
	currentSlot := slotIdx + 1

	for i := 0; i < int(span)-1; i++ {
		if currentSlot >= EntriesPerPage {
			// Would need to read from next page, but for simplicity assume fits in current page
			// In a full implementation, would handle page boundaries
			break
		}

		dataOffset := FirstEntryOffset + currentSlot*EntrySize
		if dataOffset+EntrySize > len(page) {
			break
		}

		buf = append(buf, page[dataOffset:dataOffset+EntrySize]...)
		currentSlot++
	}

	// Trim to actual length (no null terminator for blobs)
	if int(blobLen) > len(buf) {
		blobLen = uint16(len(buf))
	}
	return buf[:blobLen], nil
}

// typeToString converts an entry type byte to its string representation
func typeToString(t uint8) string {
	switch t {
	case typeU8:
		return "u8"
	case typeU16:
		return "u16"
	case typeU32:
		return "u32"
	case typeI8:
		return "i8"
	case typeI16:
		return "i16"
	case typeI32:
		return "i32"
	case typeString:
		return "string"
	case typeBlob:
		return "blob"
	default:
		return "unknown"
	}
}
