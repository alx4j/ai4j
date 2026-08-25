// Package executableprofile classifies immutable Darwin executable bytes with bounded
// random-access reads and projects native details into lifecycle-neutral facts.
package executableprofile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	maximumArchitectures = 16
	maximumShebangBytes  = 512
	maximumLoadCommands  = 4096
	maximumLoadBytes     = 16 << 20

	machOMagic32 = 0xfeedface
	machOMagic64 = 0xfeedfacf
	fatMagic32   = 0xcafebabe
	fatMagic64   = 0xcafebabf

	cpuX8664 = 0x01000007
	cpuARM64 = 0x0100000c

	cpuSubtypeARM64All = 0
	cpuSubtypeARM64V8  = 1
	cpuSubtypeARM64E   = 2
	cpuSubtypePAuthV0  = 0x80000002
	cpuSubtypePAuthV1  = 0x81000002
	cpuSubtypeX8664All = 3
	cpuSubtypeX8664H   = 8
)

// Classify reads only bounded headers. ReaderAt and size must describe the same
// immutable object; an unexpected short read is returned as an observation
// error rather than projected into a potentially stale profile.
func Classify(reader io.ReaderAt, size int64) (lifecycle.StaticExecutableProfile, error) {
	if reader == nil || size < 0 {
		return lifecycle.StaticExecutableProfile{}, fmt.Errorf("invalid executable reader")
	}
	probeSize := size
	if probeSize > 4 {
		probeSize = 4
	}
	probe, _, err := readAt(reader, size, 0, int(probeSize))
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	if len(probe) >= 2 && probe[0] == '#' && probe[1] == '!' {
		return classifyShebang(reader, size)
	}
	if size < 4 {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
	}
	bigMagic := binary.BigEndian.Uint32(probe)
	littleMagic := binary.LittleEndian.Uint32(probe)
	switch {
	case bigMagic == fatMagic32:
		return classifyFat(reader, size)
	case littleMagic == machOMagic64:
		return classifyThin(reader, size, 0, uint64(size), binary.LittleEndian, lifecycle.NativeSingleImage)
	case littleMagic == machOMagic32:
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedArchitecture)
	case bigMagic == fatMagic64 || littleMagic == fatMagic32 || littleMagic == fatMagic64 ||
		bigMagic == machOMagic32 || bigMagic == machOMagic64:
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
	default:
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
	}
}

func classifyShebang(reader io.ReaderAt, size int64) (lifecycle.StaticExecutableProfile, error) {
	readSize := size
	if readSize > maximumShebangBytes {
		readSize = maximumShebangBytes
	}
	bytes, _, err := readAt(reader, size, 0, int(readSize))
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	if len(bytes) < 2 || bytes[0] != '#' || bytes[1] != '!' {
		return lifecycle.StaticExecutableProfile{}, fmt.Errorf("executable prefix changed during classification")
	}
	lineEnd := -1
	for index, value := range bytes {
		if value == '\n' || value == '#' && index >= 2 {
			lineEnd = index
			break
		}
	}
	if lineEnd < 0 {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedShebang)
	}
	if lineEnd >= maximumShebangBytes || lineEnd < 2 {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedShebang)
	}
	line := trimShebangSpace(bytes[2:lineEnd])
	if len(line) == 0 || !safeShebangASCII(line) {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedShebang)
	}
	separator := len(line)
	for index, value := range line {
		if value == ' ' || value == '\t' {
			separator = index
			break
		}
	}
	interpreter := string(line[:separator])
	if !canonicalInterpreter(interpreter) {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedShebang)
	}
	remainder := trimShebangSpace(line[separator:])
	if interpreter != "/usr/bin/env" {
		arguments := strings.Fields(string(remainder))
		if len(arguments) > 1 {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedShebang)
		}
		fixedArgument := ""
		if len(arguments) == 1 {
			fixedArgument = arguments[0]
		}
		profile, profileErr := lifecycle.NewDirectShebangProfile(interpreter, fixedArgument)
		if profileErr != nil {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedShebang)
		}
		return lifecycle.NewScriptStaticExecutableProfile(profile)
	}
	if len(remainder) == 0 {
		return ambiguousEnv(interpreter, lifecycle.ShebangEnvMissingTarget)
	}
	if remainder[0] == '-' {
		return ambiguousEnv(interpreter, lifecycle.ShebangEnvOption)
	}
	fields := strings.Fields(string(remainder))
	if len(fields) != 1 {
		return ambiguousEnv(interpreter, lifecycle.ShebangEnvMultipleTokens)
	}
	if strings.Contains(fields[0], "=") {
		return ambiguousEnv(interpreter, lifecycle.ShebangEnvAssignment)
	}
	profile, profileErr := lifecycle.NewEnvShebangProfile(interpreter, fields[0])
	if profileErr != nil {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedShebang)
	}
	return lifecycle.NewScriptStaticExecutableProfile(profile)
}

func ambiguousEnv(interpreter string, ambiguity lifecycle.ShebangAmbiguity) (lifecycle.StaticExecutableProfile, error) {
	profile, err := lifecycle.NewAmbiguousEnvShebangProfile(interpreter, ambiguity)
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	return lifecycle.NewScriptStaticExecutableProfile(profile)
}

func classifyThin(reader io.ReaderAt, size, offset int64, imageSize uint64, order binary.ByteOrder, layout lifecycle.NativeImageLayout) (lifecycle.StaticExecutableProfile, error) {
	header, complete, err := readAt(reader, size, offset, 32)
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	if !complete {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
	}
	if order != binary.LittleEndian || binary.LittleEndian.Uint32(header[:4]) != machOMagic64 {
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
	}
	loadCount := order.Uint32(header[16:20])
	loadBytes := order.Uint32(header[20:24])
	if loadCount > maximumLoadCommands || loadBytes > maximumLoadBytes ||
		(loadCount == 0) != (loadBytes == 0) || loadCount > 0 && loadBytes < loadCount*8 {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
	}
	if imageSize < 32 || uint64(loadBytes) > imageSize-32 {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
	}
	if valid, validationErr := validateLoadCommands(reader, size, offset+32, order, loadCount, loadBytes); validationErr != nil {
		return lifecycle.StaticExecutableProfile{}, validationErr
	} else if !valid {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
	}
	architecture, ok := architectureForCPU(order.Uint32(header[4:8]), order.Uint32(header[8:12]))
	if !ok {
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedArchitecture)
	}
	role, ok := nativeRole(order.Uint32(header[12:16]))
	if !ok {
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFileRole)
	}
	native, err := lifecycle.NewNativeExecutableProfile(layout, role, architecture)
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	return lifecycle.NewNativeStaticExecutableProfile(native)
}

type fatSegment struct{ start, end uint64 }

func classifyFat(reader io.ReaderAt, size int64) (lifecycle.StaticExecutableProfile, error) {
	order := binary.BigEndian
	header, complete, err := readAt(reader, size, 0, 8)
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	if !complete {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
	}
	if order.Uint32(header[:4]) != fatMagic32 {
		return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
	}
	count := order.Uint32(header[4:8])
	if count == 0 {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
	}
	if count > maximumArchitectures {
		return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueTooManyArchitectures)
	}
	entrySize := 20
	tableBytes := uint64(count) * uint64(entrySize)
	if uint64(size) < 8 || tableBytes > uint64(size)-8 {
		return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
	}
	tableEnd := uint64(8) + tableBytes
	var architectures lifecycle.ExecutableArchitectureSet
	var role lifecycle.NativeFileRole
	segments := make([]fatSegment, 0, count)
	type cpuTuple struct{ cpu, subtype uint32 }
	tuples := make([]cpuTuple, 0, count)
	for index := uint32(0); index < count; index++ {
		entryOffset := int64(8 + uint64(index)*uint64(entrySize))
		entry, _, readErr := readAt(reader, size, entryOffset, entrySize)
		if readErr != nil {
			return lifecycle.StaticExecutableProfile{}, readErr
		}
		outerCPU := order.Uint32(entry[0:4])
		outerSubtype := order.Uint32(entry[4:8])
		architecture, known := architectureForCPU(outerCPU, outerSubtype)
		if !known {
			return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedArchitecture)
		}
		tuple := cpuTuple{cpu: outerCPU, subtype: outerSubtype}
		for _, previous := range tuples {
			if previous == tuple {
				return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
			}
		}
		tuples = append(tuples, tuple)
		architectures |= architecture
		imageOffset := uint64(order.Uint32(entry[8:12]))
		imageSize := uint64(order.Uint32(entry[12:16]))
		alignment := order.Uint32(entry[16:20])
		if alignment > 31 || imageOffset%(uint64(1)<<alignment) != 0 {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
		}
		if imageSize < 32 || imageOffset > uint64(size) || imageSize > uint64(size)-imageOffset {
			return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
		}
		if imageOffset < tableEnd {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
		}
		segment := fatSegment{start: imageOffset, end: imageOffset + imageSize}
		for _, previous := range segments {
			if segment.start < previous.end && previous.start < segment.end {
				return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
			}
		}
		segments = append(segments, segment)
		imageHeader, complete, readErr := readAt(reader, size, int64(imageOffset), 32)
		if readErr != nil {
			return lifecycle.StaticExecutableProfile{}, readErr
		}
		if !complete {
			return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
		}
		if binary.LittleEndian.Uint32(imageHeader[:4]) != machOMagic64 {
			return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFormat)
		}
		imageCPU := binary.LittleEndian.Uint32(imageHeader[4:8])
		imageSubtype := binary.LittleEndian.Uint32(imageHeader[8:12])
		if outerCPU != imageCPU || outerSubtype != imageSubtype {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
		}
		imageRole, known := nativeRole(binary.LittleEndian.Uint32(imageHeader[12:16]))
		if !known {
			return issue(lifecycle.StaticExecutableUnsupported, lifecycle.StaticIssueUnsupportedFileRole)
		}
		if index == 0 {
			role = imageRole
		} else if role != imageRole {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueInconsistentFileRole)
		}
		loadCount := binary.LittleEndian.Uint32(imageHeader[16:20])
		loadBytes := binary.LittleEndian.Uint32(imageHeader[20:24])
		if loadCount > maximumLoadCommands || loadBytes > maximumLoadBytes ||
			(loadCount == 0) != (loadBytes == 0) || loadCount > 0 && loadBytes < loadCount*8 {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
		}
		if uint64(loadBytes) > imageSize-32 {
			return issue(lifecycle.StaticExecutableTruncated, lifecycle.StaticIssueTruncatedHeader)
		}
		if valid, validationErr := validateLoadCommands(reader, size, int64(imageOffset)+32, binary.LittleEndian, loadCount, loadBytes); validationErr != nil {
			return lifecycle.StaticExecutableProfile{}, validationErr
		} else if !valid {
			return issue(lifecycle.StaticExecutableMalformed, lifecycle.StaticIssueMalformedHeader)
		}
	}
	native, err := lifecycle.NewNativeExecutableProfile(lifecycle.NativeMultiImage, role, architectures)
	if err != nil {
		return lifecycle.StaticExecutableProfile{}, err
	}
	return lifecycle.NewNativeStaticExecutableProfile(native)
}

func validateLoadCommands(reader io.ReaderAt, size, offset int64, order binary.ByteOrder, count, commandBytes uint32) (bool, error) {
	var cursor uint32
	for index := uint32(0); index < count; index++ {
		if cursor > commandBytes || commandBytes-cursor < 8 {
			return false, nil
		}
		header, complete, err := readAt(reader, size, offset+int64(cursor), 8)
		if err != nil {
			return false, err
		}
		if !complete {
			return false, nil
		}
		length := order.Uint32(header[4:8])
		if length < 8 || length%4 != 0 || length > commandBytes-cursor {
			return false, nil
		}
		cursor += length
	}
	return cursor == commandBytes, nil
}

func architectureForCPU(cpu, subtype uint32) (lifecycle.ExecutableArchitectureSet, bool) {
	switch cpu {
	case cpuARM64:
		if subtype == cpuSubtypeARM64All || subtype == cpuSubtypeARM64V8 || subtype == cpuSubtypeARM64E ||
			subtype == cpuSubtypePAuthV0 || subtype == cpuSubtypePAuthV1 {
			return lifecycle.ExecutableARM64, true
		}
	case cpuX8664:
		if subtype == cpuSubtypeX8664All || subtype == cpuSubtypeX8664H {
			return lifecycle.ExecutableX8664, true
		}
	}
	return 0, false
}

func nativeRole(value uint32) (lifecycle.NativeFileRole, bool) {
	roles := [...]lifecycle.NativeFileRole{
		0: "", 1: lifecycle.NativeObject, 2: lifecycle.NativeExecutable, 3: lifecycle.NativeFixedVMLibrary,
		4: lifecycle.NativeCore, 5: lifecycle.NativePreload, 6: lifecycle.NativeSharedLibrary,
		7: lifecycle.NativeDynamicLinker, 8: lifecycle.NativeBundle, 9: lifecycle.NativeSharedStub,
		10: lifecycle.NativeDebugSymbols, 11: lifecycle.NativeKernelExtension, 12: lifecycle.NativeFileSet,
	}
	if value >= uint32(len(roles)) || !roles[value].Valid() {
		return "", false
	}
	return roles[value], true
}

func readAt(reader io.ReaderAt, size, offset int64, count int) ([]byte, bool, error) {
	if offset < 0 || count < 0 || offset > size || int64(count) > size-offset {
		return nil, false, nil
	}
	bytes := make([]byte, count)
	read, err := reader.ReadAt(bytes, offset)
	if read == count && (err == nil || errors.Is(err, io.EOF)) {
		return bytes, true, nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return nil, false, fmt.Errorf("read executable header: %w", err)
}

func issue(kind lifecycle.StaticExecutableKind, reason lifecycle.StaticExecutableIssue) (lifecycle.StaticExecutableProfile, error) {
	return lifecycle.NewStaticExecutableIssueProfile(kind, reason)
}

func canonicalInterpreter(value string) bool {
	return path.IsAbs(value) && value != "/" && !strings.HasPrefix(value, "//") &&
		!strings.HasSuffix(value, "/") && path.Clean(value) == value
}

func trimShebangSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}

func safeShebangASCII(value []byte) bool {
	for _, character := range value {
		if character != '\t' && (character < 0x20 || character > 0x7e) {
			return false
		}
	}
	return true
}
