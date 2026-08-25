package process

import (
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

const captureReadBytes = 32 << 10

type captureResult struct {
	raw       []byte
	truncated bool
	err       error
}

// capture drains the complete stream while retaining only the exact bounded
// prefix. Draining is independent of exposure mode so opaque and text streams
// have identical backpressure and truncation semantics.
func capture(reader io.Reader, maximum int64) captureResult {
	if reader == nil || maximum <= 0 {
		return captureResult{err: errProcessCapture}
	}
	result := captureResult{raw: make([]byte, 0, minInt64(maximum, captureReadBytes))}
	buffer := make([]byte, captureReadBytes)
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			remaining := maximum - int64(len(result.raw))
			keep := int64(read)
			if keep > remaining {
				keep = remaining
			}
			if keep > 0 {
				result.raw = append(result.raw, buffer[:int(keep)]...)
			}
			if int64(read) > keep {
				result.truncated = true
			}
		}
		if err != nil {
			if err != io.EOF {
				result.err = fmt.Errorf("%w", errProcessCapture)
			}
			return result
		}
		if read == 0 {
			result.err = errProcessCapture
			return result
		}
	}
}

func processStream(mode lifecycle.ProcessOutputMode, captured captureResult) (lifecycle.ProcessStream, error) {
	if captured.err != nil {
		return lifecycle.ProcessStream{}, captured.err
	}
	normalized, ok := lifecycle.NormalizeProcessOutputMode(mode)
	if !ok {
		return lifecycle.ProcessStream{}, errInvalidProcessRequest
	}
	data := captured.raw
	if normalized == lifecycle.SanitizedTextOutput {
		data = sanitizeText(data)
	}
	stream, ok := lifecycle.NewProcessStream(normalized, data, captured.truncated)
	if !ok {
		return lifecycle.ProcessStream{}, errProcessCapture
	}
	return stream, nil
}

// sanitizeText replaces every unsafe encoded unit with one ASCII question
// mark. It preserves intentional newline/tab/carriage-return whitespace and
// never grows beyond the raw byte cap.
func sanitizeText(input []byte) []byte {
	result := make([]byte, 0, len(input))
	for len(input) > 0 {
		character, size := utf8.DecodeRune(input)
		if character == utf8.RuneError && size == 1 {
			result = append(result, '?')
			input = input[1:]
			continue
		}
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			result = append(result, '?')
			input = input[size:]
			continue
		}
		result = append(result, input[:size]...)
		input = input[size:]
	}
	return result
}

func minInt64(left int64, right int) int {
	if left < int64(right) {
		return int(left)
	}
	return right
}
