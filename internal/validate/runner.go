package validate

import (
	"context"

	"github.com/alx4j/ai4j/internal/hostprocess"
)

type processRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, string, []string, []string) (hostprocess.Result, error)
	RunIsolated(context.Context, string, string, []string, []string) (hostprocess.Result, error)
}
