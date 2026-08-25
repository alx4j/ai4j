package filesystem

import (
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

const (
	maximumDarwinDirectoryLocatorBytes = 4096
	maximumDarwinPathComponentBytes    = 255
	maximumDarwinPathComponents        = 128
)

func canonicalDarwinDirectoryLocator(value string) (string, bool) {
	if value == "" || value == "/" || len(value) > maximumDarwinDirectoryLocatorBytes ||
		!utf8.ValidString(value) || !path.IsAbs(value) || path.Clean(value) != value ||
		strings.HasSuffix(value, "/") {
		return "", false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return "", false
		}
	}
	components := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(components) > maximumDarwinPathComponents {
		return "", false
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." ||
			len(component) > maximumDarwinPathComponentBytes {
			return "", false
		}
	}
	return strings.Clone(value), true
}

// darwinDirectoryLocatorsOverlap is deliberately stricter than lexical POSIX
// containment. APFS deployments may be case- or normalization-insensitive,
// so component-prefix comparison also uses Unicode NFD plus full case fold.
func darwinDirectoryLocatorsOverlap(first, second string) bool {
	first, firstOK := canonicalDarwinDirectoryLocator(first)
	second, secondOK := canonicalDarwinDirectoryLocator(second)
	if !firstOK || !secondOK {
		return true
	}
	firstComponents := strings.Split(strings.TrimPrefix(first, "/"), "/")
	secondComponents := strings.Split(strings.TrimPrefix(second, "/"), "/")
	if componentPrefix(firstComponents, secondComponents) || componentPrefix(secondComponents, firstComponents) {
		return true
	}
	firstCollision, firstOK := darwinCollisionComponents(first)
	secondCollision, secondOK := darwinCollisionComponents(second)
	if !firstOK || !secondOK {
		return true
	}
	return componentPrefix(firstCollision, secondCollision) || componentPrefix(secondCollision, firstCollision)
}

func componentPrefix(parent, child []string) bool {
	if len(parent) > len(child) {
		return false
	}
	for index := range parent {
		if parent[index] != child[index] {
			return false
		}
	}
	return true
}

func darwinCollisionComponents(absolute string) ([]string, bool) {
	relative, err := pathsafe.NewRelativePath(strings.TrimPrefix(absolute, "/"))
	if err != nil {
		return nil, false
	}
	key, err := pathsafe.NewCollisionKey(relative)
	if err != nil || !key.Valid() || key.Version() != pathsafe.CollisionKeyVersionV1() {
		return nil, false
	}
	return strings.Split(key.Canonical(), "/"), true
}

func directoryIdentityOverlap(
	candidate []lifecycle.ObjectIdentity,
	candidatePresent bool,
	protected []lifecycle.ObjectIdentity,
	protectedPresent bool,
) bool {
	if len(candidate) == 0 || len(protected) == 0 {
		return true
	}
	if candidatePresent && identityInChain(candidate[len(candidate)-1], protected) {
		return true
	}
	return protectedPresent && identityInChain(protected[len(protected)-1], candidate)
}

func identityInChain(identity lifecycle.ObjectIdentity, chain []lifecycle.ObjectIdentity) bool {
	if !identity.Valid() {
		return true
	}
	for _, candidate := range chain {
		if candidate == identity {
			return true
		}
	}
	return false
}

func relativeDirectoryObjectIssue(
	candidate lifecycle.DirectoryObjectProof,
	home lifecycle.DirectoryObjectProof,
) lifecycle.DirectoryQualificationIssue {
	if !candidate.Valid() || !home.Valid() {
		return lifecycle.DirectoryObservationFailedIssue()
	}
	if candidate.OwnerClass() != lifecycle.CurrentUserOwner {
		return lifecycle.WrongDirectoryOwnerIssue()
	}
	mode := candidate.Mode()
	if mode.Perm()&0o700 != 0o700 || mode.Perm()&0o022 != 0 ||
		mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		return lifecycle.UnsafeDirectoryModeIssue()
	}
	if candidate.Identity().Filesystem != home.Identity().Filesystem {
		return lifecycle.UnsupportedFilesystemIssue()
	}
	return lifecycle.DirectoryQualificationIssue{}
}
