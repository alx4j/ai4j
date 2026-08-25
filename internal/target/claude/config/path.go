package config

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/pathsafe"
)

const maximumPathBytes = 4096

func validCapturedValue(value string) bool {
	if len(value) > maximumPathBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validAbsoluteDirectory(value string) bool {
	return value != "" && value != "/" && validCapturedValue(value) &&
		path.IsAbs(value) && path.Clean(value) == value
}

func beneathHome(home, absolute string) (pathsafe.RelativePath, bool) {
	if !validAbsoluteDirectory(home) || !validAbsoluteDirectory(absolute) {
		return pathsafe.RelativePath{}, false
	}
	prefix := home + "/"
	if !strings.HasPrefix(absolute, prefix) {
		return pathsafe.RelativePath{}, false
	}
	relative, err := pathsafe.NewRelativePath(strings.TrimPrefix(absolute, prefix))
	return relative, err == nil
}
