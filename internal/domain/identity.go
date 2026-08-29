package domain

import (
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	repositoryHostLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	repositoryPathPartPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
)

type RepositoryIdentity struct{ value string }

func NewRepositoryIdentity(value string) (RepositoryIdentity, error) {
	if !validRepositoryIdentity(value) {
		return RepositoryIdentity{}, fmt.Errorf("repository %q is not a canonical Git identity", value)
	}
	return RepositoryIdentity{value: value}, nil
}
func (v RepositoryIdentity) String() string { return v.value }
func (v RepositoryIdentity) Valid() bool    { return validRepositoryIdentity(v.value) }

func validRepositoryIdentity(value string) bool {
	if value == "" || len(value) > 768 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.HasSuffix(value, ".git") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	host, repositoryPath, ok := strings.Cut(value, "/")
	if !ok || host == "" || repositoryPath == "" || host != strings.ToLower(host) || len(host) > 253 || net.ParseIP(host) != nil || host == "github.com" && repositoryPath != strings.ToLower(repositoryPath) {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !repositoryHostLabelPattern.MatchString(label) {
			return false
		}
	}
	parts := strings.Split(repositoryPath, "/")
	if len(parts) == 0 || len(parts) > 32 {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || !repositoryPathPartPattern.MatchString(part) {
			return false
		}
	}
	return true
}

type CommitOID struct{ value [20]byte }

func NewCommitOID(value string) (CommitOID, error) {
	decoded, err := decodeLowerHex("commit OID", value, 20)
	if err != nil {
		return CommitOID{}, err
	}
	var oid CommitOID
	copy(oid.value[:], decoded)
	return oid, nil
}
func (v CommitOID) String() string { return hex.EncodeToString(v.value[:]) }
func (v CommitOID) Valid() bool    { return v != CommitOID{} }

type TreeOID struct{ value [20]byte }

func NewTreeOID(value string) (TreeOID, error) {
	decoded, err := decodeLowerHex("tree OID", value, 20)
	if err != nil {
		return TreeOID{}, err
	}
	var oid TreeOID
	copy(oid.value[:], decoded)
	return oid, nil
}
func (v TreeOID) String() string { return hex.EncodeToString(v.value[:]) }
func (v TreeOID) Valid() bool    { return v != TreeOID{} }

type RenderedDigest struct{ value [32]byte }

func NewRenderedDigest(value string) (RenderedDigest, error) {
	decoded, err := decodeLowerHex("rendered digest", value, 32)
	if err != nil {
		return RenderedDigest{}, err
	}
	var digest RenderedDigest
	copy(digest.value[:], decoded)
	return digest, nil
}
func (v RenderedDigest) String() string { return hex.EncodeToString(v.value[:]) }
func (v RenderedDigest) Valid() bool    { return v != RenderedDigest{} }

// BuildCommit is the CLI binary's source commit and is intentionally distinct
// from the toolkit repository commit identity.
type BuildCommit struct{ value [20]byte }

func NewBuildCommit(value string) (BuildCommit, error) {
	decoded, err := decodeLowerHex("build commit", value, 20)
	if err != nil {
		return BuildCommit{}, err
	}
	var commit BuildCommit
	copy(commit.value[:], decoded)
	return commit, nil
}
func (v BuildCommit) String() string { return hex.EncodeToString(v.value[:]) }
func (v BuildCommit) Valid() bool    { return v != BuildCommit{} }

type CommitIdentity struct {
	repository RepositoryIdentity
	format     ObjectFormat
	oid        CommitOID
}

func NewCommitIdentity(repository RepositoryIdentity, format ObjectFormat, oid CommitOID) (CommitIdentity, error) {
	if !repository.Valid() {
		return CommitIdentity{}, fmt.Errorf("invalid repository identity")
	}
	if format != SHA1ObjectFormat() {
		return CommitIdentity{}, fmt.Errorf("unsupported commit object format %q", format.String())
	}
	if !oid.Valid() {
		return CommitIdentity{}, fmt.Errorf("invalid commit OID")
	}
	return CommitIdentity{repository: repository, format: format, oid: oid}, nil
}
func (v CommitIdentity) Repository() RepositoryIdentity { return v.repository }
func (v CommitIdentity) ObjectFormat() ObjectFormat     { return v.format }
func (v CommitIdentity) OID() CommitOID                 { return v.oid }
func (v CommitIdentity) Valid() bool {
	return v.repository.Valid() && v.format == SHA1ObjectFormat() && v.oid.Valid()
}

func decodeLowerHex(kind, value string, bytes int) ([]byte, error) {
	if len(value) != bytes*2 {
		return nil, fmt.Errorf("%s must contain exactly %d lowercase hexadecimal characters", kind, bytes*2)
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return nil, fmt.Errorf("%s must contain only lowercase hexadecimal characters", kind)
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", kind, err)
	}
	allZero := true
	for _, b := range decoded {
		allZero = allZero && b == 0
	}
	if allZero {
		return nil, fmt.Errorf("%s must not be all zero", kind)
	}
	return decoded, nil
}
