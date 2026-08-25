package git

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maximumTreeRecordOverheadBytes = 63

// GitObjectOID is a generic SHA-1 object name used only while an object's
// exact Git type is still being proven. It is deliberately distinct from the
// domain's commit and tree identities.
type GitObjectOID struct{ value [20]byte }

func NewGitObjectOID(value string) (GitObjectOID, error) {
	decoded, ok := decodeObjectOID(value)
	if !ok {
		return GitObjectOID{}, ErrExecutorContract
	}
	return GitObjectOID{value: decoded}, nil
}

func (o GitObjectOID) String() string { return hex.EncodeToString(o.value[:]) }
func (o GitObjectOID) Valid() bool    { return o.value != [20]byte{} }

// BlobOID is a SHA-1 object name whose blob type was established by the exact
// ls-tree grammar. It cannot be substituted for a commit or tree identity.
type BlobOID struct{ value [20]byte }

func NewBlobOID(value string) (BlobOID, error) {
	decoded, ok := decodeObjectOID(value)
	if !ok {
		return BlobOID{}, ErrExecutorContract
	}
	return BlobOID{value: decoded}, nil
}

func (o BlobOID) String() string { return hex.EncodeToString(o.value[:]) }
func (o BlobOID) Valid() bool    { return o.value != [20]byte{} }

// SourceFileMode is the closed set of regular-file modes that may be
// materialized. Symlinks, submodules, directories, and special modes are not
// representable.
type SourceFileMode string

const (
	SourceRegularFile    SourceFileMode = "100644"
	SourceExecutableFile SourceFileMode = "100755"
)

func (m SourceFileMode) Valid() bool {
	return m == SourceRegularFile || m == SourceExecutableFile
}

// TreeEntry is one validated regular blob selected for materialization.
type TreeEntry struct {
	path pathsafe.RelativePath
	mode SourceFileMode
	oid  BlobOID
	size uint64
}

func (e TreeEntry) Path() pathsafe.RelativePath { return e.path }
func (e TreeEntry) Mode() SourceFileMode        { return e.mode }
func (e TreeEntry) OID() BlobOID                { return e.oid }
func (e TreeEntry) SizeBytes() uint64           { return e.size }

func (e TreeEntry) Valid() bool {
	return e.path.Valid() && !reservedGitMetadataPath(e.path) && e.mode.Valid() &&
		e.oid.Valid() && e.size <= MaximumBlobBytes
}

// TreeInventory is an immutable, deterministic pre-materialization proof for
// one exact tree. Formatting never discloses repository paths or object names.
type TreeInventory struct {
	tree      domain.TreeOID
	entries   []TreeEntry
	pathBytes uint64
	treeBytes uint64
}

func ParseTreeInventory(tree domain.TreeOID, data []byte) (TreeInventory, error) {
	if !tree.Valid() {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailureInvalidOperation)
	}
	records, err := protocol.ParseTree(data)
	if err != nil {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailureMalformedProtocol)
	}
	if len(records) > MaximumInventoryPathCount {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailureResourceLimit)
	}

	entries := make([]TreeEntry, 0, len(records))
	paths := make([]pathsafe.RelativePath, 0, len(records))
	var pathBytes, treeBytes uint64
	for _, record := range records {
		path, pathErr := pathsafe.NewRelativePath(record.Path)
		if pathErr != nil || reservedGitMetadataPath(path) || record.Type != "blob" || !record.SizeKnown {
			return TreeInventory{}, NewExecutorError(OperationListTree, FailurePolicyRejected)
		}
		mode := SourceFileMode(record.Mode)
		oid, oidErr := NewBlobOID(record.OID)
		if !mode.Valid() || oidErr != nil {
			return TreeInventory{}, NewExecutorError(OperationListTree, FailurePolicyRejected)
		}
		if record.Size > MaximumBlobBytes || pathBytes > MaximumInventoryPathBytes-uint64(len(record.Path)) ||
			treeBytes > MaximumValidatedTreeBytes-record.Size {
			return TreeInventory{}, NewExecutorError(OperationListTree, FailureResourceLimit)
		}
		pathBytes += uint64(len(record.Path))
		treeBytes += record.Size
		entry := TreeEntry{path: path, mode: mode, oid: oid, size: record.Size}
		if !entry.Valid() {
			return TreeInventory{}, NewExecutorError(OperationListTree, FailurePolicyRejected)
		}
		entries = append(entries, entry)
		paths = append(paths, path)
	}
	if _, found, collisionErr := pathsafe.FindPathCollision(paths); collisionErr != nil || found {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailurePolicyRejected)
	}
	if !treeProtocolFits(pathBytes, len(entries)) {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailureResourceLimit)
	}
	slices.SortFunc(entries, func(left, right TreeEntry) int {
		return strings.Compare(left.path.String(), right.path.String())
	})
	inventory := TreeInventory{tree: tree, entries: entries, pathBytes: pathBytes, treeBytes: treeBytes}
	if !inventory.Valid() {
		return TreeInventory{}, NewExecutorError(OperationListTree, FailurePolicyRejected)
	}
	return inventory, nil
}

func (i TreeInventory) Tree() domain.TreeOID { return i.tree }
func (i TreeInventory) Entries() []TreeEntry { return append([]TreeEntry(nil), i.entries...) }
func (i TreeInventory) PathCount() int       { return len(i.entries) }
func (i TreeInventory) PathBytes() uint64    { return i.pathBytes }
func (i TreeInventory) TreeBytes() uint64    { return i.treeBytes }

func (i TreeInventory) Valid() bool {
	if !i.tree.Valid() || len(i.entries) > MaximumInventoryPathCount ||
		i.pathBytes > MaximumInventoryPathBytes || i.treeBytes > MaximumValidatedTreeBytes {
		return false
	}
	paths := make([]pathsafe.RelativePath, 0, len(i.entries))
	var pathBytes, treeBytes uint64
	previous := ""
	for index, entry := range i.entries {
		if !entry.Valid() || index > 0 && strings.Compare(previous, entry.path.String()) >= 0 ||
			pathBytes > MaximumInventoryPathBytes-uint64(len(entry.path.String())) ||
			treeBytes > MaximumValidatedTreeBytes-entry.size {
			return false
		}
		pathBytes += uint64(len(entry.path.String()))
		treeBytes += entry.size
		previous = entry.path.String()
		paths = append(paths, entry.path)
	}
	if pathBytes != i.pathBytes || treeBytes != i.treeBytes || !treeProtocolFits(pathBytes, len(i.entries)) {
		return false
	}
	_, found, err := pathsafe.FindPathCollision(paths)
	return err == nil && !found
}

func (TreeInventory) String() string   { return "<git-tree-inventory:redacted>" }
func (TreeInventory) GoString() string { return "<git-tree-inventory:redacted>" }
func (i TreeInventory) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, i.String())
}
func (i TreeInventory) MarshalText() ([]byte, error) { return []byte(i.String()), nil }
func (TreeInventory) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_tree_inventory": "redacted"})
}

func treeProtocolFits(pathBytes uint64, records int) bool {
	if records < 0 || records > MaximumInventoryPathCount {
		return false
	}
	overhead := uint64(records) * maximumTreeRecordOverheadBytes
	return pathBytes <= protocol.MaximumTreeOutputBytes &&
		overhead <= protocol.MaximumTreeOutputBytes-pathBytes
}

func decodeObjectOID(value string) ([20]byte, bool) {
	var result [20]byte
	if len(value) != hex.EncodedLen(len(result)) {
		return result, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return result, false
			}
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return result, false
	}
	copy(result[:], decoded)
	return result, result != [20]byte{}
}

func reservedGitMetadataPath(path pathsafe.RelativePath) bool {
	if !path.Valid() {
		return true
	}
	for _, component := range path.Components() {
		canonical := norm.NFC.String(cases.Fold().String(stripDefaultIgnorables(component)))
		if canonical == ".git" || ntfsGitShortName(canonical) {
			return true
		}
	}
	return false
}

func stripDefaultIgnorables(value string) string {
	return strings.Map(func(character rune) rune {
		if defaultIgnorableV15(character) {
			return -1
		}
		return character
	}, value)
}

// defaultIgnorableV15 is the closed subset needed to conservatively reserve
// the .git namespace. It follows Unicode 15 Default_Ignorable_Code_Point
// ranges instead of depending on host Unicode tables.
func defaultIgnorableV15(character rune) bool {
	switch {
	case character == '\u00ad', character == '\u034f', character == '\u061c',
		character >= '\u115f' && character <= '\u1160',
		character >= '\u17b4' && character <= '\u17b5',
		character >= '\u180b' && character <= '\u180f',
		character >= '\u200b' && character <= '\u200f',
		character >= '\u202a' && character <= '\u202e',
		character >= '\u2060' && character <= '\u206f',
		character == '\u3164',
		character >= '\ufe00' && character <= '\ufe0f',
		character == '\ufeff', character == '\uffa0',
		character >= '\U0001bca0' && character <= '\U0001bca3',
		character >= '\U0001d173' && character <= '\U0001d17a',
		character >= '\U000e0000' && character <= '\U000e0fff':
		return true
	default:
		return false
	}
}

func ntfsGitShortName(value string) bool {
	value = strings.TrimPrefix(value, ".")
	if !strings.HasPrefix(value, "git~") || len(value) <= len("git~") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "git~") {
		if character < '0' || character > '9' {
			return false
		}
	}
	return strings.TrimPrefix(value, "git~")[0] != '0' && utf8.ValidString(value)
}
