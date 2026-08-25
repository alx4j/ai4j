package diskcapacity

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
)

func TestRequireUsesNearestExistingAncestor(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "child")
	if err := Require(path, 1); err != nil {
		t.Fatalf("Require() error = %v", err)
	}
}

func TestRequireRejectsImpossibleAndInvalidRequests(t *testing.T) {
	t.Parallel()
	if err := Require(t.TempDir(), math.MaxUint64); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("overflow error = %v", err)
	}
	if err := Require("relative", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("relative error = %v", err)
	}
}
