package nvs

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
)

// entry represents an internal NVS entry with computed fields
type entry struct {
	namespaceIdx uint8
	entryType    uint8
	span         uint8
	chunkIndex   uint8
	key          [16]byte
	data         [8]byte
	crc32Val     uint32
	rawData      []byte // For multi-span strings and blobs
}

// newEntry creates an entry with data field pre-filled with 0xFF,
// matching ESP-IDF's convention for unused bytes.
func newEntry() *entry {
	e := &entry{}
	for i := range e.data {
		e.data[i] = 0xFF
	}
	return e
}

// GenerateNVS creates an NVS partition binary from entries.
// partitionSize must be a multiple of PageSize.
func GenerateNVS(entries []Entry, partitionSize int) ([]byte, error) {
	// Validate partition size is a multiple of PageSize
	if partitionSize%PageSize != 0 {
		return nil, fmt.Errorf("partition size must be a multiple of %d, got %d", PageSize, partitionSize)
	}

	totalPages := partitionSize / PageSize

	// Create partition buffer filled with 0xFF
	partition := make([]byte, partitionSize)
	for i := range partition {
		partition[i] = 0xFF
	}

	// Group entries by namespace. Namespace order is recorded as first-seen
	// in the input slice (rather than ranged over the map) so GenerateNVS is
	// deterministic: Go map iteration order is randomized, which previously
	// made repeated calls with identical input produce different partition
	// layouts.
	namespaceMap := make(map[string][]*Entry)
	var nsOrder []string
	for i, e := range entries {
		if _, seen := namespaceMap[e.Namespace]; !seen {
			nsOrder = append(nsOrder, e.Namespace)
		}
		namespaceMap[e.Namespace] = append(namespaceMap[e.Namespace], &entries[i])
	}

	// Process each namespace
	pageIdx := 0
	nsCounter := uint8(0)
	for _, ns := range nsOrder {
		nsEntries := namespaceMap[ns]
		nsCounter++
		// Write namespace entry first — type is U8 with data = namespace index
		nsEntry := newEntry()
		nsEntry.namespaceIdx = 0
		nsEntry.entryType = namespaceType
		nsEntry.span = spanOne
		nsEntry.chunkIndex = singleChunkIndex
		copyKeyToEntry(ns, nsEntry)
		nsEntry.data[0] = nsCounter // namespace index stored as U8 value
		nsEntry.crc32Val = calculateEntryCRC32(nsEntry)

		// Collect all entries for this namespace
		var entriesToWrite []*entry
		entriesToWrite = append(entriesToWrite, nsEntry)

		for _, e := range nsEntries {
			ents, err := parseEntry(e, nsCounter) // use the namespace index
			if err != nil {
				return nil, err
			}
			entriesToWrite = append(entriesToWrite, ents...)
		}

		// Write entries to pages
		pagesUsed, err := writePage(&partition, pageIdx, uint32(pageIdx), entriesToWrite, totalPages)
		if err != nil {
			return nil, err
		}
		pageIdx += pagesUsed
	}

	return partition, nil
}

// parseEntry converts an Entry to one or more internal entries (for multi-span strings/blobs)
func parseEntry(e *Entry, namespaceIdx uint8) ([]*entry, error) {
	if e.Raw {
		return buildRawEntry(e, namespaceIdx)
	}

	var result []*entry

	switch e.Type {
	case "u8":
		val, ok := e.Value.(uint8)
		if !ok {
			// Try to convert from int or other numeric types
			if iv, ok := e.Value.(int); ok {
				if iv < 0 || iv > 255 {
					return nil, fmt.Errorf("u8 value out of range: %v", iv)
				}
				val = uint8(iv)
			} else {
				return nil, fmt.Errorf("invalid u8 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeU8
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		ent.data[0] = val
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "u16":
		val, ok := e.Value.(uint16)
		if !ok {
			// Try to convert from int or other numeric types
			if iv, ok := e.Value.(int); ok {
				if iv < 0 || iv > 65535 {
					return nil, fmt.Errorf("u16 value out of range: %v", iv)
				}
				val = uint16(iv)
			} else {
				return nil, fmt.Errorf("invalid u16 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeU16
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint16(ent.data[0:2], val)
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "u32":
		val, ok := e.Value.(uint32)
		if !ok {
			// Try to convert from int or other numeric types
			if iv, ok := e.Value.(int); ok {
				if iv < 0 {
					return nil, fmt.Errorf("u32 value out of range: %v", iv)
				}
				val = uint32(iv)
			} else {
				return nil, fmt.Errorf("invalid u32 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeU32
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint32(ent.data[0:4], val)
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "i8":
		val, ok := e.Value.(int8)
		if !ok {
			// Try to convert from int
			if iv, ok := e.Value.(int); ok {
				if iv < -128 || iv > 127 {
					return nil, fmt.Errorf("i8 value out of range: %v", iv)
				}
				val = int8(iv)
			} else {
				return nil, fmt.Errorf("invalid i8 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeI8
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		ent.data[0] = byte(val)
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "i16":
		val, ok := e.Value.(int16)
		if !ok {
			// Try to convert from int
			if iv, ok := e.Value.(int); ok {
				if iv < -32768 || iv > 32767 {
					return nil, fmt.Errorf("i16 value out of range: %v", iv)
				}
				val = int16(iv)
			} else {
				return nil, fmt.Errorf("invalid i16 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeI16
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint16(ent.data[0:2], uint16(val))
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "i32":
		val, ok := e.Value.(int32)
		if !ok {
			// Try to convert from int
			if iv, ok := e.Value.(int); ok {
				val = int32(iv)
			} else {
				return nil, fmt.Errorf("invalid i32 value: %v", e.Value)
			}
		}
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeI32
		ent.span = spanOne
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint32(ent.data[0:4], uint32(val))
		ent.crc32Val = calculateEntryCRC32(ent)
		result = append(result, ent)

	case "string":
		str, ok := e.Value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid string value: %v", e.Value)
		}
		strBytes := append([]byte(str), 0) // Add null terminator
		strLen := len(strBytes)

		// Calculate span: 1 header entry + ceil(strLen / EntrySize) data entries
		dataEntries := int(math.Ceil(float64(strLen) / float64(EntrySize)))
		if dataEntries == 0 {
			dataEntries = 1
		}
		span := uint8(1 + dataEntries) // header + data entries

		// Create header entry
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeString
		ent.span = span
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint16(ent.data[0:2], uint16(strLen))
		// data[2:3] already 0xFF from newEntry() (reserved field)
		// Calculate string data CRC and store in header entry
		stringCrc := calculateStringCRC32(strBytes)
		binary.LittleEndian.PutUint32(ent.data[4:8], stringCrc)
		ent.crc32Val = calculateEntryCRC32(ent)
		ent.rawData = strBytes
		result = append(result, ent)

	case "blob":
		data, ok := e.Value.([]byte)
		if !ok {
			return nil, fmt.Errorf("invalid blob value: %v", e.Value)
		}
		blobLen := len(data)

		// Calculate span: 1 header entry + ceil(blobLen / EntrySize) data entries
		dataEntries := int(math.Ceil(float64(blobLen) / float64(EntrySize)))
		if dataEntries == 0 {
			dataEntries = 1
		}
		span := uint8(1 + dataEntries) // header + data entries

		// Create header entry
		ent := newEntry()
		ent.namespaceIdx = namespaceIdx
		ent.entryType = typeBlob
		ent.span = span
		ent.chunkIndex = singleChunkIndex
		copyKeyToEntry(e.Key, ent)
		binary.LittleEndian.PutUint16(ent.data[0:2], uint16(blobLen))
		// data[2:3] already 0xFF from newEntry() (reserved field)
		// Calculate blob data CRC and store in header entry
		blobCrc := calculateStringCRC32(data)
		binary.LittleEndian.PutUint32(ent.data[4:8], blobCrc)
		ent.crc32Val = calculateEntryCRC32(ent)
		ent.rawData = data
		result = append(result, ent)

	default:
		return nil, fmt.Errorf("unknown entry type: %s", e.Type)
	}

	return result, nil
}

// buildRawEntry re-encodes a passthrough Entry (Entry.Raw == true) byte-for-
// byte, using its captured TypeByte/Span/ChunkIndex/Data instead of
// interpreting Type/Value. This is what makes read-modify-write lossless for
// entry types ParseNVS doesn't natively decode: it never needs to understand
// the value's semantics to re-emit an equivalent slot.
func buildRawEntry(e *Entry, namespaceIdx uint8) ([]*entry, error) {
	if len(e.Data) < 8 {
		return nil, fmt.Errorf("raw entry %q: Data must be at least 8 bytes (entry header), got %d", e.Key, len(e.Data))
	}

	span := e.Span
	if span == 0 {
		span = spanOne
	}
	wantContLen := (int(span) - 1) * EntrySize
	if len(e.Data) != 8+wantContLen {
		return nil, fmt.Errorf("raw entry %q: Data length %d does not match span %d (want %d)", e.Key, len(e.Data), span, 8+wantContLen)
	}

	ent := newEntry()
	ent.namespaceIdx = namespaceIdx
	ent.entryType = e.TypeByte
	ent.span = span
	ent.chunkIndex = e.ChunkIndex
	copyKeyToEntry(e.Key, ent)
	copy(ent.data[:], e.Data[:8])
	ent.crc32Val = calculateEntryCRC32(ent)
	if wantContLen > 0 {
		ent.rawData = e.Data[8:]
	}

	return []*entry{ent}, nil
}

// writePage writes entries to pages and returns number of pages written.
//
// Mirrors ESP-IDF's Page::writeItem: an item's header slot and all of its
// continuation/data slots are always written together on a single page.
// Before placing an item, the full slot count it needs is computed; if it
// doesn't fit in the current page's remaining slots, the current page is
// finalized as-is (its remaining slots stay Empty/0xFF) and the *entire*
// item is written on a fresh page instead. An item's span never crosses a
// page boundary.
func writePage(partition *[]byte, startPageNum int, seqNum uint32, entries []*entry, totalPages int) (int, error) {
	pageNum := startPageNum
	if pageNum >= totalPages {
		return 0, fmt.Errorf("not enough pages: need at least %d pages", pageNum+1)
	}
	pageOffset := pageNum * PageSize
	page := (*partition)[pageOffset : pageOffset+PageSize]

	// Initialize page with 0xFF
	for i := range page {
		page[i] = 0xFF
	}

	// Write header
	writePageHeader(page, seqNum+uint32(pageNum-startPageNum))

	// Write entry bitmap and entries
	bitmapOffset := HeaderSize
	slotIdx := 0

	startNewPage := func() error {
		pageNum++
		if pageNum >= totalPages {
			return fmt.Errorf("not enough pages: need at least %d pages", pageNum+1)
		}
		pageOffset = pageNum * PageSize
		page = (*partition)[pageOffset : pageOffset+PageSize]
		// Initialize new page with 0xFF
		for i := range page {
			page[i] = 0xFF
		}
		// Write header
		writePageHeader(page, seqNum+uint32(pageNum-startPageNum))
		slotIdx = 0
		return nil
	}

	for _, e := range entries {
		dataSlots := 0
		if e.rawData != nil {
			dataSlots = int(e.span) - 1 // header slot already accounted separately
		}
		entryCount := 1 + dataSlots // header slot + continuation/data slots

		if entryCount > EntriesPerPage {
			return 0, fmt.Errorf("entry %q: needs %d slots, which exceeds a single page's capacity (%d); ESP-IDF requires an item's header and all continuation slots to fit within one page", readNullTerminatedString(e.key[:]), entryCount, EntriesPerPage)
		}

		// Fit check: if this item (header + continuation slots, as a whole)
		// won't fit in what remains of the current page, close the current
		// page and start the whole item fresh on a new page.
		if slotIdx+entryCount > EntriesPerPage {
			if err := startNewPage(); err != nil {
				return 0, err
			}
		}

		// Mark slot as written in bitmap
		markBitmapWritten(page, bitmapOffset, slotIdx)

		// Write the entry header
		entryOffset := FirstEntryOffset + slotIdx*EntrySize
		writeEntry(page[entryOffset:entryOffset+EntrySize], e)
		slotIdx++

		// Write raw continuation data into the following slots. These are
		// guaranteed by the fit check above to remain on this same page.
		for ds := 0; ds < dataSlots; ds++ {
			markBitmapWritten(page, bitmapOffset, slotIdx)
			dataOffset := FirstEntryOffset + slotIdx*EntrySize
			// Copy chunk of raw data, rest stays 0xFF
			start := ds * EntrySize
			end := start + EntrySize
			if end > len(e.rawData) {
				end = len(e.rawData)
			}
			if start < len(e.rawData) {
				copy(page[dataOffset:dataOffset+EntrySize], e.rawData[start:end])
			}
			slotIdx++
		}
	}

	return pageNum - startPageNum + 1, nil
}

// markBitmapWritten sets the 2-bit entry state to "written" (0b10) in the bitmap.
func markBitmapWritten(page []byte, bitmapOffset int, slotIdx int) {
	bitIndex := uint(slotIdx) * 2
	byteIdx := bitmapOffset + int(bitIndex/8)
	bitOffset := bitIndex % 8
	mask := uint8(0x3) << bitOffset
	page[byteIdx] = (page[byteIdx] &^ mask) | ((entryStateWritten & 0x3) << bitOffset)
}

// writePageHeader writes the NVS page header
func writePageHeader(page []byte, seqNum uint32) {
	// Byte 0: state
	page[0] = pageStateActive

	// Bytes 1-3: reserved (0xFF)
	page[1] = 0xFF
	page[2] = 0xFF
	page[3] = 0xFF

	// Bytes 4-7: sequence number (uint32 LE)
	binary.LittleEndian.PutUint32(page[4:8], seqNum)

	// Byte 8: version
	page[8] = pageVersion

	// Bytes 9-27: reserved (0xFF)
	for i := 9; i < 28; i++ {
		page[i] = 0xFF
	}

	// Bytes 28-31: CRC32 of bytes 4-27
	binary.LittleEndian.PutUint32(page[28:32], espCRC32(page[4:28]))
}

// writeEntry writes an entry to the page
func writeEntry(entrySpace []byte, e *entry) {
	entrySpace[0] = e.namespaceIdx
	entrySpace[1] = e.entryType
	entrySpace[2] = e.span
	entrySpace[3] = e.chunkIndex

	// Bytes 4-7: CRC32
	binary.LittleEndian.PutUint32(entrySpace[4:8], e.crc32Val)

	// Bytes 8-23: key (16 bytes)
	copy(entrySpace[8:24], e.key[:])

	// Bytes 24-31: data (8 bytes)
	copy(entrySpace[24:32], e.data[:])
}

// copyKeyToEntry copies a key string to an entry, null-terminated
func copyKeyToEntry(key string, e *entry) {
	if len(key) > maxKeyLen {
		key = key[:maxKeyLen]
	}
	copy(e.key[:], key)
	e.key[len(key)] = 0 // Null terminate
}

// espCRC32 computes CRC32 matching ESP-IDF's NVS page/entry CRC.
func espCRC32(data []byte) uint32 {
	return crc32.Update(0xFFFFFFFF, crc32.IEEETable, data)
}

// calculateEntryCRC32 calculates CRC32 for an entry.
// Covers: nsIndex(1) + type(1) + span(1) + chunkIndex(1) + key(16) + data(8) = 28 bytes.
func calculateEntryCRC32(e *entry) uint32 {
	buf := make([]byte, 28)
	buf[0] = e.namespaceIdx
	buf[1] = e.entryType
	buf[2] = e.span
	buf[3] = e.chunkIndex
	copy(buf[4:20], e.key[:])
	copy(buf[20:28], e.data[:])
	return espCRC32(buf)
}

// calculateStringCRC32 calculates CRC32 for the raw string/blob data.
func calculateStringCRC32(data []byte) uint32 {
	return espCRC32(data)
}
