package validate

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
)

func validateSelectedExecutableFormats(root string, selected []resolvedAsset, host cli.BuildHost, model validatedManifest) error {
	for _, item := range selected {
		if item.asset.Type != "script" && item.asset.Type != "binary" {
			continue
		}
		if item.executable == nil || item.executable.Command != item.path {
			return validationError("invalid_executable", "toolkit-owned executable command must identify the selected file")
		}
		contents, err := readTrackedFile(root, item.path, model.tracked)
		if err != nil || validateExecutableFormat(item.path, item.asset.Type, host, contents) != nil {
			return validationError("invalid_executable", "selected executable does not match its declared host profile")
		}
	}
	return nil
}

func validateExecutableFormat(path, assetType string, host cli.BuildHost, contents []byte) error {
	if len(contents) == 0 || strings.IndexByte(string(contents), 0) >= 0 && assetType == "script" {
		return errors.New("executable is empty or malformed")
	}
	switch assetType {
	case "script":
		extension := strings.ToLower(filepath.Ext(path))
		if host == cli.BuildHostWindowsAMD64 {
			if extension != ".ps1" {
				return errors.New("Windows script must be PowerShell")
			}
			return nil
		}
		firstLine := string(contents)
		if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
			firstLine = strings.TrimSuffix(firstLine[:index], "\r")
		}
		if extension != ".sh" || firstLine != "#!/bin/sh" && firstLine != "#!/bin/bash" && firstLine != "#!/usr/bin/env sh" && firstLine != "#!/usr/bin/env bash" {
			return errors.New("Darwin script has an unsupported shebang")
		}
		return nil
	case "binary":
		if host == cli.BuildHostWindowsAMD64 {
			if len(contents) < 0x40 || string(contents[:2]) != "MZ" {
				return errors.New("Windows binary is not PE")
			}
			offset := int(binary.LittleEndian.Uint32(contents[0x3c:0x40]))
			if offset < 0 || offset+6 > len(contents) || string(contents[offset:offset+4]) != "PE\x00\x00" || binary.LittleEndian.Uint16(contents[offset+4:offset+6]) != 0x8664 {
				return errors.New("Windows binary is not AMD64 PE")
			}
			return nil
		}
		if len(contents) < 8 {
			return errors.New("Darwin binary is not Mach-O")
		}
		little := string(contents[:4]) == "\xcf\xfa\xed\xfe" && binary.LittleEndian.Uint32(contents[4:8]) == 0x0100000c
		big := string(contents[:4]) == "\xfe\xed\xfa\xcf" && binary.BigEndian.Uint32(contents[4:8]) == 0x0100000c
		if !little && !big {
			return errors.New("Darwin binary is not ARM64 Mach-O")
		}
		return nil
	default:
		return errors.New("unsupported executable asset type")
	}
}
