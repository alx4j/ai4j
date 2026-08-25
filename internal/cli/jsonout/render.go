// Package jsonout renders schema-version-1 CLI responses as JSON.
package jsonout

import (
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonv1"
	"github.com/alx4j/ai4j/internal/result"
)

// Render validates and fully materializes one JSON document before making
// exactly one call to output. The returned exit code always comes from the
// typed command result when the response is valid.
func Render(output io.Writer, response cli.Response) (result.ExitCode, error) {
	if !response.Valid() {
		return result.ExitUnexpectedInternal, fmt.Errorf("render JSON: invalid response")
	}
	exitCode := response.Result().ExitCode()
	if output == nil {
		return exitCode, fmt.Errorf("render JSON: output is required")
	}

	document, err := jsonv1.Marshal(response)
	if err != nil {
		return exitCode, fmt.Errorf("render JSON: %w", err)
	}
	document = append(document, '\n')

	written, err := output.Write(document)
	if err != nil {
		return exitCode, fmt.Errorf("render JSON: %w", err)
	}
	if written != len(document) {
		return exitCode, fmt.Errorf("render JSON: %w", io.ErrShortWrite)
	}
	return exitCode, nil
}
