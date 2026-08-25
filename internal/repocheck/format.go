package repocheck

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// TrackedGoFiles returns the repository-owned Go paths selected by Git.
func TrackedGoFiles(ctx context.Context, root string) ([]string, error) {
	output, err := run(ctx, root, "git", "ls-files", "-z", "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("list tracked Go files: %w", err)
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		paths = append(paths, string(part))
	}
	return paths, nil
}

// CheckFormat reports tracked Go source that differs from canonical gofmt output.
func CheckFormat(root string, paths []string) error {
	var drift []string
	for _, relative := range paths {
		nativePath := filepath.FromSlash(relative)
		if strings.Contains(relative, `\`) || path.IsAbs(relative) || filepath.IsAbs(nativePath) || filepath.VolumeName(nativePath) != "" || path.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("tracked Go path %q escapes the repository", relative)
		}
		filePath := filepath.Join(root, nativePath)
		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("inspect tracked Go path %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("tracked Go path %s is not a bounded regular file", relative)
		}
		contents, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read tracked Go path %s: %w", relative, err)
		}
		formatted, err := format.Source(contents)
		if err != nil {
			return fmt.Errorf("format %s: %w", relative, err)
		}
		if !bytes.Equal(contents, formatted) {
			drift = append(drift, filepath.ToSlash(relative))
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("Go formatting drift: %s", strings.Join(drift, ", "))
	}
	return nil
}
