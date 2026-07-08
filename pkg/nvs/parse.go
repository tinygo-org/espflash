package nvs

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// rawSlotEntry is the structural (type-agnostic) decode of one NVS entry
// record: namespace/key/type/span/chunkIndex plus the 8-byte header "data"
// field. It does not yet know the namespace *name* (only its index) because
// the namespace declaration for that index may live on a page scanned later
// than the key that references it.
type rawSlotEntry struct {
	pageNum      int
	slotIdx      int
	namespaceIdx uint8
	entryType    uint8
	span         uint8
	chunkIndex   uint8
	key          string
	headerData   []byte // 8 bytes, entryBytes[24:32]
}

// pageRecord is a validated page ready for the structural scan, tagged with
// its physical index and the sequence number from its header.
type pageRecord struct {
	pageNum int
	seqNum  uint32
	page    []byte
}

// ParseNVS reads an NVS partition binary and returns a flat slice of entries.
// It handles:
//   - Page validation (state, version, header CRC)
//   - Entry bitmap decoding
//   - Namespace mapping, independent of scan order (a key may be declared
//     before its namespace's declaration entry, e.g. on a page scanned later)
//   - Multi-slot entries (strings and blobs); per ESP-IDF's on-flash format an
//     item's header + all continuation slots always live on a single page, so
//     each is read entirely within its own page
//   - Value decoding based on type, with lossless generic passthrough for any
//     type this codec does not natively model (see Entry.Raw)
//   - Deduplication (last write wins), keyed by namespace+key+chunkIndex so
//     chunked entries (e.g. esp_wifi blob-index credentials) that legitimately
//     share a key are not collapsed into one. Pages are scanned in ascending
//     order of their header sequence number (not physical/flash order, which
//     ESP-IDF explicitly does not guarantee reflects write chronology), so
//     later-seq writes naturally overwrite earlier ones for the same key.
func ParseNVS(data []byte) ([]Entry, error) {
	// Validate data length is a multiple of PageSize
	if len(data)%PageSize != 0 {
		return nil, fmt.Errorf("data length (%d) must be a multiple of PageSize (%d)", len(data), PageSize)
	}

	totalPages := len(data) / PageSize

	// Pass 1: validate each page and collect the ones worth scanning,
	// tagged with their header sequence number.
	var pages []pageRecord
	for pageNum := 0; pageNum < totalPages; pageNum++ {
		pageOffset := pageNum * PageSize
		page := data[pageOffset : pageOffset+PageSize]

		state := page[0]
		if state == pageStateEmpty {
			continue
		}

		if page[8] != pageVersion {
			continue
		}

		// Validate header CRC32: bytes 4-27, result at bytes 28-31
		expectedCRC := binary.LittleEndian.Uint32(page[28:32])
		actualCRC := espCRC32(page[4:28])
		if actualCRC != expectedCRC {
			return nil, fmt.Errorf("page %d header CRC mismatch: expected 0x%x, got 0x%x", pageNum, expectedCRC, actualCRC)
		}

		seqNum := binary.LittleEndian.Uint32(page[4:8])
		pages = append(pages, pageRecord{pageNum: pageNum, seqNum: seqNum, page: page})
	}

	// Scan pages in ascending sequence-number order, not physical order:
	// ESP-IDF decouples page write chronology from physical placement, so the
	// sequence number in the page header is the only reliable ordering for
	// "last write wins" semantics. Ties (which shouldn't occur in a valid
	// partition) fall back to physical order for determinism.
	sort.SliceStable(pages, func(i, j int) bool {
		if pages[i].seqNum != pages[j].seqNum {
			return pages[i].seqNum < pages[j].seqNum
		}
		return pages[i].pageNum < pages[j].pageNum
	})

	// Map from namespace index to namespace name. Populated as namespace
	// declarations are encountered during the structural walk below; only
	// consulted after that walk completes, so scan order never causes a key
	// to be dropped for "namespace not yet defined".
	namespaceMap := make(map[uint8]string)

	// Structural walk: decode every written entry record on every valid
	// page, in sequence-number order. Namespace declarations are resolved
	// immediately (they're never namespaced themselves); everything else is
	// queued for phase 2.
	var rawEntries []rawSlotEntry

	for _, pr := range pages {
		page := pr.page
		pageNum := pr.pageNum

		processedSlots := make(map[int]bool) // Track multi-slot entries

		for slotIdx := 0; slotIdx < EntriesPerPage; slotIdx++ {
			// Skip if already processed as part of a multi-slot entry
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
			if span == 0 {
				span = 1
			}
			chunkIndex := entryBytes[3]
			// crc32Val := binary.LittleEndian.Uint32(entryBytes[4:8]) // TODO: validate CRC
			key := readNullTerminatedString(entryBytes[8:24])
			headerData := append([]byte(nil), entryBytes[24:32]...)

			// Mark slots used by this entry's continuation as processed.
			// ESP-IDF never splits an item across pages: an item's header +
			// continuation slots are always fully contained within one page
			// (see Page::writeItem in nvs_page.cpp). If a span byte claims
			// more continuation slots than remain on this page, that layout
			// cannot come from a real ESP-IDF partition; treat it as
			// corrupt and clamp to what's actually present on this page
			// rather than reaching into the next page's slots.
			contNeeded := int(span) - 1
			s := slotIdx + 1
			for contNeeded > 0 && s < EntriesPerPage {
				processedSlots[s] = true
				s++
				contNeeded--
			}
			if contNeeded > 0 {
				span -= uint8(contNeeded)
			}

			// Handle namespace entries (namespaceIdx == 0)
			if namespaceIdx == 0 && entryType == namespaceType {
				nsIndex := headerData[0]
				namespaceMap[nsIndex] = key
				continue
			}

			rawEntries = append(rawEntries, rawSlotEntry{
				pageNum:      pageNum,
				slotIdx:      slotIdx,
				namespaceIdx: namespaceIdx,
				entryType:    entryType,
				span:         span,
				chunkIndex:   chunkIndex,
				key:          key,
				headerData:   headerData,
			})
		}
	}

	// Phase 2: resolve namespace names (now fully known, regardless of the
	// scan order in which declarations vs. keys were encountered) and decode
	// each entry's value, deduplicating on (namespace, key, chunkIndex) so
	// last-write-wins semantics hold without collapsing distinct chunks of a
	// chunked value that happen to share a key. rawEntries is already in
	// ascending page-sequence order, so a later overwrite of an earlier map
	// entry here means "higher sequence number wins".
	type dedupKey struct {
		namespace  string
		key        string
		chunkIndex uint8
	}
	entryMap := make(map[dedupKey]*Entry)
	var order []dedupKey // first-seen order, for stable output

	for _, re := range rawEntries {
		namespaceName, ok := namespaceMap[re.namespaceIdx]
		if !ok {
			// Orphaned key: no namespace declaration found anywhere in the
			// partition for this index. Nothing to resolve it to; drop it.
			continue
		}

		var value interface{}
		var err error
		raw := false

		pageOffset := re.pageNum * PageSize
		page := data[pageOffset : pageOffset+PageSize]

		switch re.entryType {
		case typeU8:
			value = re.headerData[0]

		case typeU16:
			value = binary.LittleEndian.Uint16(re.headerData[0:2])

		case typeU32:
			value = binary.LittleEndian.Uint32(re.headerData[0:4])

		case typeI8:
			value = int8(re.headerData[0])

		case typeI16:
			value = int16(binary.LittleEndian.Uint16(re.headerData[0:2]))

		case typeI32:
			value = int32(binary.LittleEndian.Uint32(re.headerData[0:4]))

		case typeString:
			value, err = readStringEntry(page, re.slotIdx, re.headerData, re.span)
			if err != nil {
				return nil, err
			}

		case typeBlob:
			value, err = readBlobEntry(page, re.slotIdx, re.headerData, re.span)
			if err != nil {
				return nil, err
			}

		default:
			// Unknown/unmodeled type: capture generically instead of
			// dropping it, so a read-modify-write round trip is lossless
			// even for entries this codec doesn't semantically understand
			// (e.g. ESP-IDF blob-index/blob-data chunk entries).
			raw = true
		}

		dk := dedupKey{namespace: namespaceName, key: re.key, chunkIndex: re.chunkIndex}
		if _, exists := entryMap[dk]; !exists {
			order = append(order, dk)
		}

		if raw {
			spanData := readSpanData(page, re.slotIdx, re.span)
			rawData := make([]byte, 0, len(re.headerData)+len(spanData))
			rawData = append(rawData, re.headerData...)
			rawData = append(rawData, spanData...)
			entryMap[dk] = &Entry{
				Namespace:  namespaceName,
				Key:        re.key,
				Type:       "raw",
				Raw:        true,
				TypeByte:   re.entryType,
				Span:       re.span,
				ChunkIndex: re.chunkIndex,
				Data:       rawData,
			}
			continue
		}

		entryMap[dk] = &Entry{
			Namespace:  namespaceName,
			Key:        re.key,
			Type:       typeToString(re.entryType),
			Value:      value,
			ChunkIndex: re.chunkIndex,
		}
	}

	// Convert map to flat slice in first-seen order (values reflect the last
	// write per the deduplication above).
	result := make([]Entry, 0, len(order))
	for _, dk := range order {
		result = append(result, *entryMap[dk])
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

// readSpanData reads the raw continuation payload for an entry that occupies
// `span` 32-byte slots (header slot included), starting at slotIdx, entirely
// within `page`. ESP-IDF never splits an item's entries across pages (see
// Page::writeItem in nvs_page.cpp): if an item doesn't fit in the remaining
// slots of the current page, the whole item — header and all continuation
// slots together — is written on a fresh page instead. So a span is always
// read within a single page; it never reaches into a neighboring page.
func readSpanData(page []byte, slotIdx int, span uint8) []byte {
	remaining := int(span) - 1
	if remaining <= 0 {
		return nil
	}

	var buf []byte
	slot := slotIdx + 1
	for i := 0; i < remaining && slot < EntriesPerPage; i, slot = i+1, slot+1 {
		dataOffset := FirstEntryOffset + slot*EntrySize
		if dataOffset+EntrySize > len(page) {
			break
		}
		buf = append(buf, page[dataOffset:dataOffset+EntrySize]...)
	}

	return buf
}

// readStringEntry reads a string entry's value from within a single page.
func readStringEntry(page []byte, slotIdx int, headerData []byte, span uint8) (string, error) {
	strLen := binary.LittleEndian.Uint16(headerData[0:2])
	if strLen == 0 {
		return "", nil
	}

	buf := readSpanData(page, slotIdx, span)

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

// readBlobEntry reads a blob entry's value from within a single page.
func readBlobEntry(page []byte, slotIdx int, headerData []byte, span uint8) ([]byte, error) {
	blobLen := binary.LittleEndian.Uint16(headerData[0:2])
	if blobLen == 0 {
		return []byte{}, nil
	}

	buf := readSpanData(page, slotIdx, span)

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
