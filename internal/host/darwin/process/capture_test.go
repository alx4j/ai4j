package process

import (
	"bytes"
	"errors"
	"testing"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

func TestCaptureDrainsAndPreservesExactOpaquePrefix(t *testing.T) {
	input := []byte{'a', 0, 'b', 0xff, 'c', 'd'}
	captured := capture(bytes.NewReader(input), 4)
	if captured.err != nil || !captured.truncated || !bytes.Equal(captured.raw, input[:4]) {
		t.Fatalf("capture = %#v", captured)
	}
	stream, err := processStream(lifecycle.OpaqueBytesOutput, captured)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := stream.OpaqueBytes()
	if !ok || !bytes.Equal(got, input[:4]) || !stream.Truncated() {
		t.Fatalf("opaque = %v, %t", got, ok)
	}
}

func TestSanitizedCaptureIsValidBoundedText(t *testing.T) {
	input := []byte{'o', 'k', 0, 0xc2, 0x80, 0xff, '\n', '\t', '\r'}
	captured := capture(bytes.NewReader(input), int64(len(input)))
	stream, err := processStream(lifecycle.SanitizedTextOutput, captured)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := stream.SanitizedText()
	if !ok || got != "ok???\n\t\r" || len(got) > len(input) {
		t.Fatalf("sanitized = %q, %t", got, ok)
	}
}

func TestDefaultOutputModeNormalizesToSanitizedText(t *testing.T) {
	stream, err := processStream("", capture(bytes.NewReader([]byte("safe")), 4))
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := stream.SanitizedText(); !ok || text != "safe" {
		t.Fatalf("stream = %q, %t", text, ok)
	}
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

func TestCaptureRejectsNonProgressingAndInvalidInputs(t *testing.T) {
	for _, captured := range []captureResult{capture(nil, 1), capture(bytes.NewReader(nil), 0), capture(zeroReader{}, 1)} {
		if !errors.Is(captured.err, errProcessCapture) {
			t.Fatalf("capture error = %v", captured.err)
		}
	}
}
