//go:build darwin && arm64

package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"golang.org/x/sys/unix"
)

const minimumAuthorityDescriptor = 64

func openAbsoluteDirectoryDescriptor(absolute string, expected lifecycle.ObjectIdentity) (*os.File, error) {
	return openAbsoluteDirectoryDescriptorWithHook(absolute, expected, nil)
}

func openAbsoluteDirectoryDescriptorWithHook(absolute string, expected lifecycle.ObjectIdentity, beforeOpen func(string)) (*os.File, error) {
	clean, err := validateAbsoluteDirectory(absolute)
	if err != nil {
		return nil, err
	}
	rootFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	currentFD := rootFD
	components := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	currentPath := "/"
	for index, component := range components {
		currentPath = path.Join(currentPath, component)
		if beforeOpen != nil {
			beforeOpen(currentPath)
		}
		openedFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		if openErr != nil {
			_ = unix.Close(rootFD)
			return nil, openErr
		}
		currentFD = openedFD
		if index == len(components)-1 {
			break
		}
	}
	_ = unix.Close(rootFD)
	file := os.NewFile(uintptr(currentFD), clean)
	if file == nil {
		_ = unix.Close(currentFD)
		return nil, errors.New("create directory descriptor")
	}
	facts, err := inspectOpenFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if facts.kind != lifecycle.DirectoryResource || expected.Valid() && facts.identity != expected {
		_ = file.Close()
		return nil, conflict("configured_root", "identity_changed", nil)
	}
	return file, nil
}

// absoluteDirectoryIdentityChain returns the opened identity of the filesystem
// root and every directory component through the requested authority. It never
// follows a symlink. Callers use the target identities against the opposite
// chain to reject aliases and containment even when path spellings differ by
// case or Unicode normalization on the mounted filesystem.
func absoluteDirectoryIdentityChain(absolute string, uid uint32) ([]lifecycle.ObjectIdentity, error) {
	clean, err := validateAbsoluteDirectory(absolute)
	if err != nil {
		return nil, err
	}
	rootFD, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	currentFD := rootFD
	defer func() {
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		_ = unix.Close(rootFD)
	}()

	identity := func(fd int) (lifecycle.ObjectIdentity, error) {
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return lifecycle.ObjectIdentity{}, err
		}
		facts := fileFactsFromUnixStat(&stat)
		if !safeAuthorityAncestor(facts, uid) || !facts.identity.Valid() {
			return lifecycle.ObjectIdentity{}, conflict("configured_root", "unsafe_ancestor", nil)
		}
		return facts.identity, nil
	}

	rootIdentity, err := identity(rootFD)
	if err != nil {
		return nil, err
	}
	chain := []lifecycle.ObjectIdentity{rootIdentity}
	for _, component := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		openedFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return nil, openErr
		}
		if currentFD != rootFD {
			_ = unix.Close(currentFD)
		}
		currentFD = openedFD
		openedIdentity, inspectErr := identity(openedFD)
		if inspectErr != nil {
			return nil, inspectErr
		}
		chain = append(chain, openedIdentity)
	}
	return chain, nil
}

func openParentNoFollow(root *rootedDirectory, name string, uid uint32, afterClassify func(string)) (*os.File, lifecycle.ObjectIdentity, error) {
	parentName := path.Dir(name)
	var descriptorLimit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &descriptorLimit); err != nil {
		return nil, lifecycle.ObjectIdentity{}, err
	}
	if descriptorLimit.Cur <= minimumAuthorityDescriptor {
		return nil, lifecycle.ObjectIdentity{}, fmt.Errorf("descriptor limit cannot reserve authority descriptor")
	}
	duplicate, err := unix.FcntlInt(root.directory.Fd(), unix.F_DUPFD_CLOEXEC, minimumAuthorityDescriptor)
	if err != nil {
		return nil, lifecycle.ObjectIdentity{}, err
	}
	current := os.NewFile(uintptr(duplicate), root.absolute)
	if parentName == "." {
		facts, inspectErr := inspectOpenFile(current)
		if inspectErr != nil {
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, inspectErr
		}
		safe := safeWritableDirectory(facts, uid)
		if root.private {
			safe = safePrivateDirectory(facts, uid)
		}
		if facts.identity != root.identity || !safe {
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, conflict("parent", "root_changed", nil)
		}
		return current, facts.identity, nil
	}
	currentPath := ""
	for _, component := range strings.Split(parentName, "/") {
		var listedStat unix.Stat_t
		if err := unix.Fstatat(int(current.Fd()), component, &listedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, fmt.Errorf("classify rooted parent: %w", err)
		}
		listed := fileFactsFromUnixStat(&listedStat)
		if listedStat.Mode&unix.S_IFMT == unix.S_IFLNK || !safeWritableDirectory(listed, uid) ||
			listed.identity.Filesystem != root.identity.Filesystem {
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, conflict("parent", "unsafe_type_or_mount", nil)
		}
		if currentPath == "" {
			currentPath = component
		} else {
			currentPath += "/" + component
		}
		if afterClassify != nil {
			afterClassify(currentPath)
		}
		openedFD, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, fmt.Errorf("open rooted parent: %w", err)
		}
		next := os.NewFile(uintptr(openedFD), currentPath)
		opened, err := inspectOpenFile(next)
		if err != nil {
			_ = next.Close()
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, err
		}
		if opened.identity != listed.identity || !safeWritableDirectory(opened, uid) ||
			opened.identity.Filesystem != root.identity.Filesystem {
			_ = next.Close()
			_ = current.Close()
			return nil, lifecycle.ObjectIdentity{}, conflict("parent", "identity_changed", nil)
		}
		_ = current.Close()
		current = next
	}
	facts, err := inspectOpenFile(current)
	if err != nil {
		_ = current.Close()
		return nil, lifecycle.ObjectIdentity{}, err
	}
	return current, facts.identity, nil
}

func classifyLeafNoFollow(parent *os.File, name string) (fileFacts, error) {
	var listedStat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &listedStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fileFacts{}, err
	}
	if listedStat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fileFacts{}, conflict("resource", "symlink", nil)
	}
	return fileFactsFromUnixStat(&listedStat), nil
}

func openLeafNoFollow(parent *os.File, name string, kind lifecycle.ResourceKind, afterClassify func()) (*os.File, fileFacts, error) {
	listed, err := classifyLeafNoFollow(parent, name)
	if err != nil {
		return nil, fileFacts{}, err
	}
	if !kindMatches(kind, listed) {
		return nil, fileFacts{}, conflict("resource", "wrong_type", nil)
	}
	if afterClassify != nil {
		afterClassify()
	}
	// First acquire a metadata-only descriptor. A raced FIFO/device therefore
	// cannot block or activate its data path before identity/type rejection.
	metadataFD, err := unix.Openat(int(parent.Fd()), name, unix.O_EVTONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fileFacts{}, err
	}
	absolute := filepath.Join(parent.Name(), name)
	metadata := os.NewFile(uintptr(metadataFD), absolute)
	metadataFacts, metadataErr := inspectOpenFile(metadata)
	_ = metadata.Close()
	if metadataErr != nil {
		return nil, fileFacts{}, metadataErr
	}
	if metadataFacts.identity != listed.identity || !kindMatches(kind, metadataFacts) {
		return nil, fileFacts{}, conflict("resource", "identity_changed", nil)
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if kind == lifecycle.DirectoryResource {
		flags |= unix.O_DIRECTORY
	} else {
		flags |= unix.O_NONBLOCK
	}
	openedFD, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		return nil, fileFacts{}, err
	}
	file := os.NewFile(uintptr(openedFD), absolute)
	opened, err := inspectOpenFile(file)
	if err != nil {
		_ = file.Close()
		return nil, fileFacts{}, err
	}
	if opened.identity != listed.identity || !kindMatches(kind, opened) {
		_ = file.Close()
		return nil, fileFacts{}, conflict("resource", "identity_changed", nil)
	}
	return file, opened, nil
}

func createLeafNoFollow(parent *os.File, name string, mode fs.FileMode) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name,
		unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func removeLeaf(parent *os.File, name string) error {
	return unix.Unlinkat(int(parent.Fd()), name, 0)
}
