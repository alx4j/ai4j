package app

import (
	"bufio"
	"errors"
	"io"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/human"
)

type approvalDecision string

const (
	approvalMissing  approvalDecision = "missing"
	approvalDeclined approvalDecision = "declined"
	approvalGranted  approvalDecision = "granted"
)

func promptApproval(commandIO CommandIO, preview cli.Response, prompt string) (approvalDecision, error) {
	if !commandIO.Interactive || commandIO.Input == nil || commandIO.Output == nil {
		return approvalMissing, nil
	}
	if _, err := human.Render(commandIO.Output, preview); err != nil {
		return approvalMissing, err
	}
	if _, err := io.WriteString(commandIO.Output, prompt); err != nil {
		return approvalMissing, err
	}
	line, err := bufio.NewReader(io.LimitReader(commandIO.Input, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return approvalMissing, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return approvalGranted, nil
	}
	return approvalDeclined, nil
}
