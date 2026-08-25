package environment

import (
	"unicode"
	"unicode/utf8"
)

const (
	maximumVersionBytes   = 128
	maximumProfileIDBytes = 64
	maximumPathBytes      = 4096
)

func validBoundedText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
