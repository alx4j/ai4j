package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

const (
	toolkitManifestPath = "toolkit.json"
	maximumMetadataSize = 1 << 20
)

type mcpManifest struct {
	Servers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Type    string            `json:"type"`
}

type packageResult struct {
	content            []cli.ContentItem
	rules              []byte
	rulesChecksum      string
	digest             string
	nativePackagePaths []string
	model              validatedManifest
}

func readStrictJSON(root, path string, tracked map[string]gitsource.TreeEntry, target any) error {
	entry, ok := tracked[path]
	if !ok || entry.SizeBytes() > maximumMetadataSize || !safeRelative(path) {
		return errors.New("metadata is not a bounded tracked file")
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumMetadataSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("metadata contains trailing data")
	}
	return nil
}

func digestFiles(root string, paths []string, tracked map[string]gitsource.TreeEntry) (string, error) {
	paths = append([]string(nil), paths...)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	digest := sha256.New()
	for _, path := range paths {
		entry, ok := tracked[path]
		if !ok || !safeRelative(path) {
			return "", errors.New("untracked content")
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || uint64(len(content)) != entry.SizeBytes() {
			return "", errors.New("content mismatch")
		}
		fileDigest := sha256.Sum256(content)
		_, _ = digest.Write([]byte(path))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(fileDigest[:])
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func readTrackedFile(root, path string, tracked map[string]gitsource.TreeEntry) ([]byte, error) {
	entry, ok := tracked[path]
	if !ok || entry.SizeBytes() > maximumMetadataSize || !safeRelative(path) {
		return nil, errors.New("content is not a bounded tracked file")
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || uint64(len(content)) != entry.SizeBytes() {
		return nil, errors.New("content mismatch")
	}
	return content, nil
}

func filesUnder(tracked map[string]gitsource.TreeEntry, root string) []string {
	prefix := strings.TrimSuffix(root, "/") + "/"
	var result []string
	for path := range tracked {
		if strings.HasPrefix(path, prefix) {
			result = append(result, path)
		}
	}
	slices.Sort(result)
	return result
}

func environmentNames(values map[string]string) ([]string, error) {
	result := make([]string, 0, len(values))
	for name, value := range values {
		if !validEnvironmentName(name) || value != "${"+name+"}" {
			return nil, validationError("literal_secret", "MCP environment values must be same-name references")
		}
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}

func argumentPlaceholders(arguments []string) ([]cli.Placeholder, error) {
	seen := map[cli.Placeholder]bool{}
	for _, argument := range arguments {
		for _, marker := range []cli.Placeholder{cli.PlaceholderPluginRoot, cli.PlaceholderProjectDir} {
			if strings.Contains(argument, string(marker)) {
				seen[marker] = true
			}
		}
		withoutKnown := strings.ReplaceAll(strings.ReplaceAll(argument, string(cli.PlaceholderPluginRoot), ""), string(cli.PlaceholderProjectDir), "")
		if strings.Contains(withoutKnown, "${") {
			return nil, validationError("invalid_placeholder", "MCP argument contains an unsupported placeholder")
		}
	}
	var result []cli.Placeholder
	for marker := range seen {
		result = append(result, marker)
	}
	slices.Sort(result)
	return result, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for _, character := range value[1:] {
		if !(character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func validHostCommand(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}

func safeRelative(value string) bool {
	if value == "" || strings.Contains(value, "\\") || filepath.IsAbs(value) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../")
}

type packageValidationError struct {
	code    string
	message string
}

func (e packageValidationError) Error() string { return e.code + ": " + e.message }

func validationError(code, message string) error {
	return packageValidationError{code: code, message: message}
}

func packageProblem(err error) (string, string) {
	var validation packageValidationError
	if errors.As(err, &validation) {
		return validation.code, validation.message
	}
	return "package_validation_failed", "toolkit package validation failed"
}
