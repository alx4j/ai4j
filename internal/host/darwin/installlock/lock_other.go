//go:build !darwin && !windows

package installlock

import "context"

func acquire(context.Context, string) (*Handle, error) { return nil, ErrUnsupported }
