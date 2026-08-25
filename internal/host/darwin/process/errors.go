package process

import "errors"

var (
	errInvalidProcessPolicy   = errors.New("invalid Darwin process policy")
	errInvalidProcessRequest  = errors.New("invalid process request")
	errProcessPolicyViolation = errors.New("process policy rejected request")
	errExecutableChanged      = errors.New("qualified executable changed")
	errUnsupportedExecutable  = errors.New("qualified executable is not runnable on Darwin arm64")
	errProcessStart           = errors.New("start qualified process")
	errProcessCapture         = errors.New("capture qualified process output")
	errProcessTeardown        = errors.New("qualified process teardown incomplete")
	errRequestTimeout         = errors.New("qualified process request timeout")
)
