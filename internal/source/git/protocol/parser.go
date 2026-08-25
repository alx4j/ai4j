// Package protocol parses the bounded machine-readable output emitted by the
// closed Git operations used by AI4J. It never formats, logs, or retains the
// original output buffer.
package protocol

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaximumRemoteOutputBytes    = 8 << 20
	MaximumTreeOutputBytes      = 16 << 20
	MaximumIndexOutputBytes     = 16 << 20
	MaximumAttributeOutputBytes = 2 << 20
	MaximumScalarOutputBytes    = 4 << 10
	MaximumConfigOutputBytes    = 64 << 10
	MaximumStatusOutputBytes    = 16 << 20
	MaximumRemoteReferenceBytes = 1024

	maximumRemoteRecords = 1 << 15
	maximumTreeRecords   = 1 << 14
	maximumIndexRecords  = 1 << 14
	maximumAttributeRows = 128 * 6
	maximumProtocolField = 4 << 10
)

var ErrMalformed = errors.New("malformed bounded Git protocol output")

// RemoteRecord is one ls-remote record. SymrefTarget is set only for a
// `ref: ...` record; otherwise OID contains a lower-case SHA-1 object name.
type RemoteRecord struct {
	OID          string
	SymrefTarget string
	Reference    string
}

// TreeRecord is one `ls-tree -rz --long` record.
type TreeRecord struct {
	Mode      string
	Type      string
	OID       string
	Size      uint64
	SizeKnown bool
	Path      string
}

// IndexRecord is one `ls-files --stage -z` record.
type IndexRecord struct {
	Mode  string
	OID   string
	Stage uint8
	Path  string
}

// AttributeRecord is one `check-attr -z` path/name/value triple.
type AttributeRecord struct {
	Path  string
	Name  string
	Value string
}

// ConfigRecord is one `git config --null --list` key/value record. The exact
// command emits a single LF between key and value and NUL between records.
type ConfigRecord struct {
	Key   string
	Value string
}

func ParseRemote(data []byte) ([]RemoteRecord, error) {
	if len(data) > MaximumRemoteOutputBytes {
		return nil, ErrMalformed
	}
	if len(data) == 0 {
		return []RemoteRecord{}, nil
	}
	if data[len(data)-1] != '\n' || bytes.IndexByte(data, 0) >= 0 ||
		bytes.Count(data, []byte{'\n'}) > maximumRemoteRecords {
		return nil, ErrMalformed
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	if len(lines) > maximumRemoteRecords {
		return nil, ErrMalformed
	}
	result := make([]RemoteRecord, 0, len(lines))
	for _, line := range lines {
		if line == "" || strings.ContainsRune(line, '\r') {
			return nil, ErrMalformed
		}
		left, right, ok := strings.Cut(line, "\t")
		if !ok || strings.ContainsRune(right, '\t') || !safeReferenceField(right) {
			return nil, ErrMalformed
		}
		record := RemoteRecord{Reference: strings.Clone(right)}
		if target, symref := strings.CutPrefix(left, "ref: "); symref {
			if !safeReferenceField(target) {
				return nil, ErrMalformed
			}
			record.SymrefTarget = strings.Clone(target)
		} else if lowerHex(left, 40) {
			record.OID = strings.Clone(left)
		} else {
			return nil, ErrMalformed
		}
		result = append(result, record)
	}
	return result, nil
}

func ParseTree(data []byte) ([]TreeRecord, error) {
	if len(data) > MaximumTreeOutputBytes {
		return nil, ErrMalformed
	}
	records, err := splitNullRecords(data, maximumTreeRecords)
	if err != nil {
		return nil, err
	}
	result := make([]TreeRecord, 0, len(records))
	for _, raw := range records {
		metadata, path, ok := strings.Cut(raw, "\t")
		if !ok || strings.ContainsRune(path, '\t') || !safeField(path) {
			return nil, ErrMalformed
		}
		fields := treeMetadataFields(metadata)
		if len(fields) != 4 || !octalMode(fields[0]) || !safeToken(fields[1]) ||
			!lowerHex(fields[2], 40) || fields[3] != "-" && !canonicalDecimal(fields[3]) {
			return nil, ErrMalformed
		}
		var size uint64
		sizeKnown := fields[3] != "-"
		if fields[1] == "blob" && !sizeKnown ||
			(fields[1] == "tree" || fields[1] == "commit") && sizeKnown ||
			fields[1] != "blob" && fields[1] != "tree" && fields[1] != "commit" {
			return nil, ErrMalformed
		}
		if sizeKnown {
			var parseErr error
			size, parseErr = strconv.ParseUint(fields[3], 10, 64)
			if parseErr != nil {
				return nil, ErrMalformed
			}
		}
		result = append(result, TreeRecord{
			Mode: strings.Clone(fields[0]), Type: strings.Clone(fields[1]), OID: strings.Clone(fields[2]),
			Size: size, SizeKnown: sizeKnown, Path: strings.Clone(path),
		})
	}
	return result, nil
}

func ParseIndex(data []byte) ([]IndexRecord, error) {
	if len(data) > MaximumIndexOutputBytes {
		return nil, ErrMalformed
	}
	records, err := splitNullRecords(data, maximumIndexRecords)
	if err != nil {
		return nil, err
	}
	result := make([]IndexRecord, 0, len(records))
	for _, raw := range records {
		metadata, path, ok := strings.Cut(raw, "\t")
		if !ok || strings.ContainsRune(path, '\t') || !safeField(path) {
			return nil, ErrMalformed
		}
		fields := exactSpaceFields(metadata, 3, 49)
		if len(fields) != 3 || !octalMode(fields[0]) || !lowerHex(fields[1], 40) ||
			len(fields[2]) != 1 || fields[2][0] < '0' || fields[2][0] > '3' {
			return nil, ErrMalformed
		}
		result = append(result, IndexRecord{
			Mode: strings.Clone(fields[0]), OID: strings.Clone(fields[1]), Stage: fields[2][0] - '0',
			Path: strings.Clone(path),
		})
	}
	return result, nil
}

func ParseAttributes(data []byte) ([]AttributeRecord, error) {
	if len(data) > MaximumAttributeOutputBytes {
		return nil, ErrMalformed
	}
	fields, err := splitNullRecords(data, maximumAttributeRows*3)
	if err != nil || len(fields)%3 != 0 {
		return nil, ErrMalformed
	}
	result := make([]AttributeRecord, 0, len(fields)/3)
	for index := 0; index < len(fields); index += 3 {
		if !safeField(fields[index]) || !safeToken(fields[index+1]) || !safeToken(fields[index+2]) {
			return nil, ErrMalformed
		}
		result = append(result, AttributeRecord{
			Path: strings.Clone(fields[index]), Name: strings.Clone(fields[index+1]), Value: strings.Clone(fields[index+2]),
		})
	}
	return result, nil
}

func ParseConfig(data []byte) ([]ConfigRecord, error) {
	if len(data) > MaximumConfigOutputBytes {
		return nil, ErrMalformed
	}
	records, err := splitNullRecords(data, 512)
	if err != nil {
		return nil, err
	}
	result := make([]ConfigRecord, 0, len(records))
	for _, raw := range records {
		key, value, ok := strings.Cut(raw, "\n")
		if !ok || strings.ContainsRune(value, '\n') || !safeConfigKey(key) || !safeField(value) {
			return nil, ErrMalformed
		}
		result = append(result, ConfigRecord{Key: strings.Clone(key), Value: strings.Clone(value)})
	}
	return result, nil
}

// ParseCleanStatus accepts the only post-materialization porcelain-v1 state
// permitted by the executor: no tracked, untracked, ignored, renamed, or
// unmerged records. It intentionally never retains hostile path records.
func ParseCleanStatus(data []byte) error {
	if len(data) > MaximumStatusOutputBytes || len(data) != 0 {
		return ErrMalformed
	}
	return nil
}

// ParseSingleLine accepts one bounded printable UTF-8 machine token with an
// optional final LF. CRLF, additional lines, NUL, and surrounding whitespace
// are rejected so callers never need to normalize native output.
func ParseSingleLine(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaximumScalarOutputBytes || data[len(data)-1] == '\r' {
		return "", ErrMalformed
	}
	if data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	value := string(data)
	if !safeField(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrMalformed
	}
	return value, nil
}

func splitNullRecords(data []byte, maximum int) ([]string, error) {
	if len(data) == 0 {
		return []string{}, nil
	}
	if data[len(data)-1] != 0 || bytes.Count(data, []byte{0}) > maximum {
		return nil, ErrMalformed
	}
	parts := strings.Split(string(data[:len(data)-1]), "\x00")
	if len(parts) > maximum {
		return nil, ErrMalformed
	}
	for _, part := range parts {
		if part == "" || !utf8.ValidString(part) {
			return nil, ErrMalformed
		}
	}
	return parts, nil
}

func exactSpaceFields(value string, count, maximumBytes int) []string {
	if value == "" || len(value) > maximumBytes || !utf8.ValidString(value) ||
		strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") || strings.Contains(value, "  ") {
		return nil
	}
	for _, character := range value {
		if character != ' ' && (character < '!' || character > '~') {
			return nil
		}
	}
	fields := strings.Split(value, " ")
	if len(fields) != count {
		return nil
	}
	return fields
}

func treeMetadataFields(value string) []string {
	if value == "" || len(value) > 80 || !utf8.ValidString(value) {
		return nil
	}
	fields := strings.SplitN(value, " ", 4)
	if len(fields) != 4 || fields[0] == "" || fields[1] == "" || fields[2] == "" || fields[3] == "" {
		return nil
	}
	size := strings.TrimLeft(fields[3], " ")
	wantPadding := 0
	if len(size) < 7 {
		wantPadding = 7 - len(size)
	}
	if size == "" || strings.ContainsRune(size, ' ') || len(fields[3])-len(size) != wantPadding {
		return nil
	}
	for _, field := range fields[:3] {
		for _, character := range field {
			if character < '!' || character > '~' {
				return nil
			}
		}
	}
	fields[3] = size
	return fields
}

func safeField(value string) bool {
	if value == "" || len(value) > maximumProtocolField || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func safeToken(value string) bool {
	return safeField(value) && !strings.ContainsAny(value, " \t\r\n\x00")
}

func safeReferenceField(value string) bool {
	return len(value) <= MaximumRemoteReferenceBytes && safeToken(value)
}

func safeConfigKey(value string) bool {
	if value == "" || len(value) > 256 || value[0] == '.' || value[len(value)-1] == '.' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' ||
			character == '.' || character == '-' {
			continue
		}
		return false
	}
	return strings.ContainsRune(value, '.')
}

func lowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func octalMode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '7' {
			return false
		}
	}
	return true
}

func canonicalDecimal(value string) bool {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
