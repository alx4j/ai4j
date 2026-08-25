package discovery

import (
	"strings"

	"github.com/alx4j/ai4j/internal/environment"
)

const (
	gitVersionPrefix    = "git version "
	appleGitPrefix      = " (Apple Git-"
	claudeVersionSuffix = " (Claude Code)"
)

func parseVersionOutput(tool environment.Tool, output string) (environment.ToolVersion, bool) {
	line, ok := singleVersionLine(output)
	if !ok {
		return environment.ToolVersion{}, false
	}
	switch tool {
	case environment.GitTool():
		return parseGitVersion(line)
	case environment.ClaudeTool():
		return parseClaudeVersion(line)
	default:
		return environment.ToolVersion{}, false
	}
}

func singleVersionLine(output string) (string, bool) {
	if output == "" || len(output) > int(probeOutputLimitBytes) {
		return "", false
	}
	switch {
	case strings.HasSuffix(output, "\r\n"):
		output = output[:len(output)-2]
	case strings.HasSuffix(output, "\n"):
		output = output[:len(output)-1]
	}
	if output == "" || strings.ContainsAny(output, "\r\n") {
		return "", false
	}
	return output, true
}

func parseGitVersion(line string) (environment.ToolVersion, bool) {
	if !strings.HasPrefix(line, gitVersionPrefix) {
		return environment.ToolVersion{}, false
	}
	payload := strings.TrimPrefix(line, gitVersionPrefix)
	if strings.HasSuffix(payload, ")") {
		marker := strings.Index(payload, appleGitPrefix)
		if marker <= 0 || strings.Count(payload, appleGitPrefix) != 1 {
			return environment.ToolVersion{}, false
		}
		semantic, semanticErr := environment.NewSemanticVersion(payload[:marker])
		revisionText := payload[marker+len(appleGitPrefix) : len(payload)-1]
		revision, revisionErr := environment.NewAppleGitRevision(revisionText)
		if semanticErr != nil || revisionErr != nil {
			return environment.ToolVersion{}, false
		}
		version, err := environment.NewAppleGitToolVersion(semantic, revision)
		return version, err == nil
	}
	semantic, err := environment.NewSemanticVersion(payload)
	if err != nil {
		return environment.ToolVersion{}, false
	}
	version, err := environment.NewSemanticToolVersion(environment.GitTool(), semantic)
	return version, err == nil
}

func parseClaudeVersion(line string) (environment.ToolVersion, bool) {
	if !strings.HasSuffix(line, claudeVersionSuffix) {
		return environment.ToolVersion{}, false
	}
	semantic, err := environment.NewSemanticVersion(strings.TrimSuffix(line, claudeVersionSuffix))
	if err != nil {
		return environment.ToolVersion{}, false
	}
	version, err := environment.NewSemanticToolVersion(environment.ClaudeTool(), semantic)
	return version, err == nil
}
