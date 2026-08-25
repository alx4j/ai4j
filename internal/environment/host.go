package environment

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
)

// OperatingSystem is a closed normalized host operating system.
type OperatingSystem struct{ value uint8 }

var darwinOperatingSystem = OperatingSystem{value: 1}

// DarwinOperatingSystem returns the only operating system supported by the MVP.
func DarwinOperatingSystem() OperatingSystem { return darwinOperatingSystem }

// NewOperatingSystem parses a normalized host operating-system observation.
func NewOperatingSystem(value string) (OperatingSystem, error) {
	if value != "darwin" {
		return OperatingSystem{}, newValidationError(CodeInvalidOperatingSystem)
	}
	return darwinOperatingSystem, nil
}

// String returns the canonical operating-system identifier.
func (s OperatingSystem) String() string {
	if s == darwinOperatingSystem {
		return "darwin"
	}
	return "invalid"
}

// Valid reports whether the value is a registered operating system.
func (s OperatingSystem) Valid() bool { return s == darwinOperatingSystem }

// Architecture is a closed normalized host architecture.
type Architecture struct{ value uint8 }

var arm64Architecture = Architecture{value: 1}

// ARM64Architecture returns the only architecture supported by the MVP.
func ARM64Architecture() Architecture { return arm64Architecture }

// NewArchitecture parses a normalized host-architecture observation.
func NewArchitecture(value string) (Architecture, error) {
	if value != "arm64" {
		return Architecture{}, newValidationError(CodeInvalidArchitecture)
	}
	return arm64Architecture, nil
}

// String returns the canonical architecture identifier.
func (a Architecture) String() string {
	if a == arm64Architecture {
		return "arm64"
	}
	return "invalid"
}

// Valid reports whether the value is a registered architecture.
func (a Architecture) Valid() bool { return a == arm64Architecture }

// DarwinVersion is a canonical Apple product version. It accepts two or three
// decimal components and preserves whether the patch component was observed.
type DarwinVersion struct {
	components uint8
	values     [3]uint32
}

// NewDarwinVersion parses canonical major.minor or major.minor.patch text.
func NewDarwinVersion(value string) (DarwinVersion, error) {
	parts, ok := parseDecimalComponents(value, 2, 3)
	if !ok || parts[0] == 0 {
		return DarwinVersion{}, newValidationError(CodeInvalidDarwinVersion)
	}
	return DarwinVersion{components: uint8(len(parts)), values: [3]uint32{parts[0], parts[1], valueAt(parts, 2)}}, nil
}

// Major returns the major Apple product-version component.
func (v DarwinVersion) Major() uint32 { return v.values[0] }

// Minor returns the minor Apple product-version component.
func (v DarwinVersion) Minor() uint32 { return v.values[1] }

// Patch returns the patch component and whether it was present in the trusted observation.
func (v DarwinVersion) Patch() (uint32, bool) { return v.values[2], v.components == 3 }

// Components returns whether the canonical observation had two or three components.
func (v DarwinVersion) Components() int { return int(v.components) }

// Valid reports whether the value is a canonical Darwin product version.
func (v DarwinVersion) Valid() bool {
	if v.components != 2 && v.components != 3 || v.values[0] == 0 {
		return false
	}
	if v.components == 2 && v.values[2] != 0 {
		return false
	}
	parsed, err := NewDarwinVersion(v.String())
	return err == nil && parsed == v
}

// String returns the canonical product-version text while preserving component count.
func (v DarwinVersion) String() string {
	if v.components != 2 && v.components != 3 {
		return "invalid"
	}
	parts := []string{strconv.FormatUint(uint64(v.values[0]), 10), strconv.FormatUint(uint64(v.values[1]), 10)}
	if v.components == 3 {
		parts = append(parts, strconv.FormatUint(uint64(v.values[2]), 10))
	}
	return strings.Join(parts, ".")
}

// MarshalText emits the canonical product-version text.
func (v DarwinVersion) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, newValidationError(CodeInvalidDarwinVersion)
	}
	return []byte(v.String()), nil
}

// HostTuple is the immutable supported host identity.
type HostTuple struct {
	host         domain.Host
	os           OperatingSystem
	architecture Architecture
	version      DarwinVersion
}

// NewHostTuple constructs the supported Darwin/ARM64 host tuple.
func NewHostTuple(host domain.Host, operatingSystem OperatingSystem, architecture Architecture, version DarwinVersion) (HostTuple, error) {
	if host != domain.DarwinHost() || operatingSystem != darwinOperatingSystem || architecture != arm64Architecture || !version.Valid() {
		return HostTuple{}, newValidationError(CodeInvalidHostTuple)
	}
	return HostTuple{host: host, os: operatingSystem, architecture: architecture, version: version}, nil
}

// Host returns the registered host identity.
func (h HostTuple) Host() domain.Host { return h.host }

// OperatingSystem returns the normalized operating system.
func (h HostTuple) OperatingSystem() OperatingSystem { return h.os }

// Architecture returns the normalized architecture.
func (h HostTuple) Architecture() Architecture { return h.architecture }

// Version returns the trusted Darwin product version.
func (h HostTuple) Version() DarwinVersion { return h.version }

// Valid reports whether the tuple is the complete supported host shape.
func (h HostTuple) Valid() bool {
	candidate, err := NewHostTuple(h.host, h.os, h.architecture, h.version)
	return err == nil && candidate == h
}

func (h HostTuple) String() string {
	if !h.Valid() {
		return "invalid"
	}
	return h.os.String() + "/" + h.architecture.String() + "/" + h.version.String()
}

// Format emits only normalized non-sensitive host facts.
func (h HostTuple) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, h.String()) }

// MarshalJSON emits only normalized non-sensitive host facts.
func (h HostTuple) MarshalJSON() ([]byte, error) {
	if !h.Valid() {
		return nil, newValidationError(CodeInvalidHostTuple)
	}
	return json.Marshal(struct {
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		Version string `json:"version"`
	}{OS: h.os.String(), Arch: h.architecture.String(), Version: h.version.String()})
}

func parseDecimalComponents(value string, minimum, maximum int) ([]uint32, bool) {
	if !validBoundedText(value, maximumVersionBytes) {
		return nil, false
	}
	text := strings.Split(value, ".")
	if len(text) < minimum || len(text) > maximum {
		return nil, false
	}
	values := make([]uint32, len(text))
	for index, component := range text {
		if component == "" || len(component) > 1 && component[0] == '0' {
			return nil, false
		}
		for _, character := range component {
			if character < '0' || character > '9' {
				return nil, false
			}
		}
		parsed, err := strconv.ParseUint(component, 10, 32)
		if err != nil {
			return nil, false
		}
		values[index] = uint32(parsed)
	}
	return values, true
}

func valueAt(values []uint32, index int) uint32 {
	if index >= len(values) {
		return 0
	}
	return values[index]
}
