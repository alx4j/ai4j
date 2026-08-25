package validate

import (
	"encoding/binary"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
)

func TestExecutableFormatsMatchSupportedHostProfiles(t *testing.T) {
	pe := make([]byte, 0x80)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[0x3c:], 0x40)
	copy(pe[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(pe[0x44:], 0x8664)
	macho := []byte{'\xcf', '\xfa', '\xed', '\xfe', 0x0c, 0x00, 0x00, 0x01}
	tests := []struct {
		name, path, kind string
		host             cli.BuildHost
		contents         []byte
		valid            bool
	}{
		{name: "Windows PowerShell", path: "tool.ps1", kind: "script", host: cli.BuildHostWindowsAMD64, contents: []byte("git diff --check\n"), valid: true},
		{name: "Windows rejects shell", path: "tool.sh", kind: "script", host: cli.BuildHostWindowsAMD64, contents: []byte("#!/bin/sh\n"), valid: false},
		{name: "Darwin shell", path: "tool.sh", kind: "script", host: cli.BuildHostDarwinARM64, contents: []byte("#!/bin/sh\n"), valid: true},
		{name: "Windows AMD64 PE", path: "tool.exe", kind: "binary", host: cli.BuildHostWindowsAMD64, contents: pe, valid: true},
		{name: "Windows rejects x86 PE", path: "tool.exe", kind: "binary", host: cli.BuildHostWindowsAMD64, contents: append(append([]byte(nil), pe[:0x44]...), 0x4c, 0x01), valid: false},
		{name: "Darwin ARM64 Mach-O", path: "tool", kind: "binary", host: cli.BuildHostDarwinARM64, contents: macho, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExecutableFormat(test.path, test.kind, test.host, test.contents)
			if (err == nil) != test.valid {
				t.Fatalf("validation error = %v, valid=%t", err, test.valid)
			}
		})
	}
}
