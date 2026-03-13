package espflasher

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"go.bug.st/serial"
)

// SLIP (Serial Line Internet Protocol) framing constants.
// The ESP bootloader uses SLIP to frame packets over UART.
const (
	slipEnd    byte = 0xC0 // Frame delimiter
	slipEsc    byte = 0xDB // Escape character
	slipEscEnd byte = 0xDC // Escaped 0xC0
	slipEscEsc byte = 0xDD // Escaped 0xDB
)

// slipEncode wraps data in a SLIP frame, escaping special bytes.
//
// Frame format: [0xC0] [escaped data] [0xC0]
//   - 0xC0 in data → 0xDB 0xDC
//   - 0xDB in data → 0xDB 0xDD
func slipEncode(data []byte) []byte {
	escaped := bytes.ReplaceAll(data, []byte{slipEsc}, []byte{slipEsc, slipEscEsc})
	escaped = bytes.ReplaceAll(escaped, []byte{slipEnd}, []byte{slipEsc, slipEscEnd})

	frame := make([]byte, 0, len(escaped)+2)
	frame = append(frame, slipEnd)
	frame = append(frame, escaped...)
	frame = append(frame, slipEnd)
	return frame
}

// slipDecode removes SLIP framing and unescapes special bytes.
func slipDecode(frame []byte) []byte {
	result := make([]byte, 0, len(frame))
	inEscape := false

	for _, b := range frame {
		if inEscape {
			switch b {
			case slipEscEnd:
				result = append(result, slipEnd)
			case slipEscEsc:
				result = append(result, slipEsc)
			default:
				// Invalid escape sequence, include as-is
				result = append(result, slipEsc, b)
			}
			inEscape = false
			continue
		}

		switch b {
		case slipEsc:
			inEscape = true
		case slipEnd:
			// Skip frame delimiters
		default:
			result = append(result, b)
		}
	}
	return result
}

// slipReader reads complete SLIP frames from a serial port.
type slipReader struct {
	port serial.Port
}

// newSlipReader creates a SLIP frame reader for the given serial port.
func newSlipReader(port serial.Port) *slipReader {
	return &slipReader{port: port}
}

// ReadFrame reads a single SLIP-framed packet from the serial port.
// It blocks until a complete frame is received or the timeout expires.
func (r *slipReader) ReadFrame(timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var partial []byte
	inFrame := false
	inEscape := false

	buf := make([]byte, 256)

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		readTimeout := min(remaining, 100*time.Millisecond)
		r.port.SetReadTimeout(readTimeout)

		n, err := r.port.Read(buf)
		if err != nil && err != io.EOF {
			// On timeout, continue; on real error, return
			if n == 0 {
				continue
			}
		}
		if n == 0 {
			continue
		}

		for i := range n {
			b := buf[i]

			if !inFrame {
				if b == slipEnd {
					inFrame = true
					partial = partial[:0] // reset
				}
				continue
			}

			if inEscape {
				inEscape = false
				switch b {
				case slipEscEnd:
					partial = append(partial, slipEnd)
				case slipEscEsc:
					partial = append(partial, slipEsc)
				default:
					return nil, fmt.Errorf("invalid SLIP escape: 0xDB 0x%02X", b)
				}
				continue
			}

			switch b {
			case slipEnd:
				if len(partial) > 0 {
					result := make([]byte, len(partial))
					copy(result, partial)
					return result, nil
				}
				// Empty frame, keep reading
			case slipEsc:
				inEscape = true
			default:
				partial = append(partial, b)
			}
		}
	}

	return nil, &TimeoutError{Op: "SLIP read"}
}
