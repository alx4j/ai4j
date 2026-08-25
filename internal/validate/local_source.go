package validate

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

var (
	errLocalSourceInvalid = errors.New("local development source is invalid")
	errLocalSourceDirty   = errors.New("local development source is dirty")
)

type localFile struct {
	path    string
	content []byte
	mode    gitsource.SourceFileMode
}

func (s Service) acquireLocal(ctx context.Context, workspace string, options cli.SourceOptions) (acquisition, error) {
	root, err := canonicalLocalRoot(options.Checkout())
	if err != nil {
		return acquisition{}, errLocalSourceInvalid
	}
	if inside(root, workspace) {
		return acquisition{}, errLocalSourceInvalid
	}
	gitExecutable, err := s.config.Runner.LookPath("git")
	if err != nil {
		return acquisition{}, errLocalSourceInvalid
	}
	top, err := s.config.Runner.Run(ctx, root, gitExecutable, []string{"rev-parse", "--show-toplevel"}, gitAuthenticatedEnvironment())
	if err != nil || top.ExitCode != 0 || len(top.Stderr) != 0 {
		return acquisition{}, errLocalSourceInvalid
	}
	topRoot, err := canonicalLocalRoot(strings.TrimSpace(string(top.Stdout)))
	if err != nil || topRoot != root {
		return acquisition{}, errLocalSourceInvalid
	}
	status, err := s.config.Runner.Run(ctx, root, gitExecutable, []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--"}, gitAuthenticatedEnvironment())
	if err != nil || status.ExitCode != 0 || len(status.Stderr) != 0 {
		return acquisition{}, errLocalSourceInvalid
	}
	dirty := len(status.Stdout) != 0
	if dirty && !options.AllowDirty() {
		return acquisition{}, errLocalSourceDirty
	}
	files, err := snapshotLocalFiles(root)
	if err != nil {
		return acquisition{}, errLocalSourceInvalid
	}
	var required uint64
	for _, file := range files {
		if required > ^uint64(0)-uint64(len(file.content)) {
			return acquisition{}, errLocalSourceInvalid
		}
		required += uint64(len(file.content))
	}
	if err := s.config.Capacity(workspace, required); err != nil {
		return acquisition{}, err
	}
	var protocol strings.Builder
	digest := sha256.New()
	for _, file := range files {
		if err := writeLocalSnapshotFile(workspace, file); err != nil {
			return acquisition{}, errLocalSourceInvalid
		}
		blobInput := append([]byte(fmt.Sprintf("blob %d\x00", len(file.content))), file.content...)
		blob := sha1.Sum(blobInput)
		_, _ = fmt.Fprintf(&protocol, "%s blob %x %7d\t%s\x00", file.mode, blob, len(file.content), file.path)
		writeDigestPart(digest, file.path)
		writeDigestPart(digest, string(file.mode))
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(file.content)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write(file.content)
	}
	sourceDigest := hex.EncodeToString(digest.Sum(nil))
	treeHash := sha1.Sum([]byte("ai4j-local-tree\x00" + sourceDigest))
	tree, err := domain.NewTreeOID(hex.EncodeToString(treeHash[:]))
	if err != nil {
		return acquisition{}, errLocalSourceInvalid
	}
	inventory, err := gitsource.ParseTreeInventory(tree, []byte(protocol.String()))
	if err != nil {
		return acquisition{}, errLocalSourceInvalid
	}
	return acquisition{inventory: inventory, checkout: root, sourceDigest: sourceDigest, dirty: dirty}, nil
}

func canonicalLocalRoot(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errLocalSourceInvalid
	}
	return filepath.Clean(resolved), nil
}

func inside(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func snapshotLocalFiles(root string) ([]localFile, error) {
	var files []localFile
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == "." || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errLocalSourceInvalid
		}
		info, err := entry.Info()
		if err != nil || info.Size() < 0 || uint64(info.Size()) > gitsource.MaximumBlobBytes {
			return errLocalSourceInvalid
		}
		content, err := os.ReadFile(path)
		if err != nil || int64(len(content)) != info.Size() {
			return errLocalSourceInvalid
		}
		mode := gitsource.SourceRegularFile
		if info.Mode()&0o111 != 0 {
			mode = gitsource.SourceExecutableFile
		}
		files = append(files, localFile{path: filepath.ToSlash(relative), content: content, mode: mode})
		return nil
	})
	if err != nil || len(files) == 0 {
		return nil, errLocalSourceInvalid
	}
	slices.SortFunc(files, func(left, right localFile) int { return strings.Compare(left.path, right.path) })
	return files, nil
}

func writeLocalSnapshotFile(root string, file localFile) error {
	destination := filepath.Join(root, filepath.FromSlash(file.path))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if file.mode == gitsource.SourceExecutableFile {
		mode = 0o700
	}
	return os.WriteFile(destination, file.content, mode)
}

func writeDigestPart(digest interface{ Write([]byte) (int, error) }, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write([]byte(value))
}

func localSourceErrorCode(err error) string {
	if errors.Is(err, errLocalSourceDirty) {
		return "dirty_source_requires_approval"
	}
	return "source_acquisition_failed"
}

func localSourceErrorMessage(err error) string {
	if errors.Is(err, errLocalSourceDirty) {
		return "local development source has dirty, untracked, or ignored input; use --allow-dirty to approve it"
	}
	return "source could not be acquired as an immutable operation snapshot"
}
