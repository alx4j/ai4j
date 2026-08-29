package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
)

const (
	AggregateOperationTimeout = 5 * time.Minute
	LocalCommandTimeout       = 15 * time.Second
	NetworkCommandTimeout     = 60 * time.Second

	MaximumReferenceCount       = 1 << 15
	MaximumReferenceBytes       = 1024
	MaximumInventoryPathCount   = 1 << 14
	MaximumInventoryPathBytes   = 14 << 20
	MaximumBlobBytes            = 64 << 20
	MaximumValidatedTreeBytes   = 512 << 20
	MaximumAttributeBatchPaths  = 128
	MaximumAttributeBatchBytes  = 128 << 10
	MaximumCheckoutAttributes   = 6
	MaximumCommandArguments     = 256
	MaximumCommandArgumentBytes = 4096
)

var ErrExecutorContract = errors.New("invalid Git executor contract")

// FailureCode is the closed, non-data-bearing executor fault taxonomy.
type FailureCode string

const (
	FailureInvalidOperation         FailureCode = "invalid_operation"
	FailurePolicyRejected           FailureCode = "policy_rejected"
	FailureCommand                  FailureCode = "command_failed"
	FailureMalformedProtocol        FailureCode = "malformed_protocol"
	FailureResourceLimit            FailureCode = "resource_limit"
	FailureRepositoryConflict       FailureCode = "repository_conflict"
	FailureReferenceNotFound        FailureCode = "reference_not_found"
	FailureReferenceAmbiguous       FailureCode = "reference_ambiguous"
	FailureDefaultBranchUnavailable FailureCode = "default_branch_unavailable"
)

func (c FailureCode) Valid() bool {
	switch c {
	case FailureInvalidOperation, FailurePolicyRejected, FailureCommand, FailureMalformedProtocol,
		FailureResourceLimit, FailureRepositoryConflict:
		return true
	case FailureReferenceNotFound, FailureReferenceAmbiguous, FailureDefaultBranchUnavailable:
		return true
	default:
		return false
	}
}

// ExecutorError preserves a stable classification without retaining Git
// output, repository locators, paths, credentials, or native error strings.
type ExecutorError struct {
	operation Operation
	code      FailureCode
}

func NewExecutorError(operation Operation, code FailureCode) error {
	if !operation.Valid() || !code.Valid() {
		return ErrExecutorContract
	}
	return ExecutorError{operation: operation, code: code}
}

func (e ExecutorError) Error() string {
	if !e.operation.Valid() || !e.code.Valid() {
		return "Git source operation failed"
	}
	return "Git source operation " + string(e.operation) + " failed: " + string(e.code)
}

func (e ExecutorError) Operation() Operation { return e.operation }
func (e ExecutorError) Code() FailureCode    { return e.code }
func (e ExecutorError) Is(target error) bool {
	other, ok := target.(ExecutorError)
	return ok && e.operation == other.operation && e.code == other.code
}

// Operation is the closed command family accepted by the Git executor.
type Operation string

const (
	OperationInitialize       Operation = "initialize"
	OperationEnumerateRefs    Operation = "enumerate_refs"
	OperationFetch            Operation = "fetch"
	OperationObjectType       Operation = "object_type"
	OperationPeelCommit       Operation = "peel_commit"
	OperationCommitTree       Operation = "commit_tree"
	OperationListTree         Operation = "list_tree"
	OperationReadTree         Operation = "read_tree"
	OperationCheckAttributes  Operation = "check_attributes"
	OperationCheckoutIndex    Operation = "checkout_index"
	OperationCheckoutDetached Operation = "checkout_detached"
	OperationListIndex        Operation = "list_index"
	OperationStatus           Operation = "status"
	OperationIsAncestor       Operation = "is_ancestor"
)

func (o Operation) Valid() bool {
	switch o {
	case OperationInitialize, OperationEnumerateRefs, OperationFetch,
		OperationObjectType, OperationPeelCommit, OperationCommitTree, OperationListTree,
		OperationReadTree, OperationCheckAttributes, OperationCheckoutIndex, OperationCheckoutDetached,
		OperationListIndex, OperationStatus, OperationIsAncestor:
		return true
	default:
		return false
	}
}

// WorkingDirectoryClass tells the caller which already-qualified directory
// expectation a command needs. It is not a path locator.
type WorkingDirectoryClass string

const (
	WorkspaceRootDirectory WorkingDirectoryClass = "workspace_root"
	GitDirectory           WorkingDirectoryClass = "git_directory"
)

func (c WorkingDirectoryClass) Valid() bool {
	return c == WorkspaceRootDirectory || c == GitDirectory
}

// OutputGrammar identifies which strict parser, if any, owns stdout bytes.
type OutputGrammar string

const (
	NoOutputGrammar        OutputGrammar = "none"
	RemoteOutputGrammar    OutputGrammar = "ls_remote"
	ScalarOutputGrammar    OutputGrammar = "scalar"
	TreeOutputGrammar      OutputGrammar = "ls_tree"
	IndexOutputGrammar     OutputGrammar = "ls_files_stage"
	AttributeOutputGrammar OutputGrammar = "check_attr"
	StatusOutputGrammar    OutputGrammar = "status_porcelain_v1"
)

func (g OutputGrammar) Valid() bool {
	switch g {
	case NoOutputGrammar, RemoteOutputGrammar, ScalarOutputGrammar, TreeOutputGrammar,
		IndexOutputGrammar, AttributeOutputGrammar, StatusOutputGrammar:
		return true
	default:
		return false
	}
}

// AuthenticationMode is the complete credential-free projection. SSH agent
// and caller-supplied helper modes are intentionally absent.
type AuthenticationMode string

const (
	AuthenticationAnonymousHTTPS        AuthenticationMode = "https_anonymous"
	AuthenticationCredentialHelperHTTPS AuthenticationMode = "https_credential_helper"
	AuthenticationDefaultKeySSH         AuthenticationMode = "ssh_default_keys"
)

func (m AuthenticationMode) Valid() bool {
	return m == AuthenticationAnonymousHTTPS || m == AuthenticationCredentialHelperHTTPS || m == AuthenticationDefaultKeySSH
}

// AuthenticationProjection binds one canonical repository to exactly one
// transport/authentication policy without retaining credentials or endpoints.
type AuthenticationProjection struct {
	repository domain.RepositoryIdentity
	transport  domain.GitTransport
	mode       AuthenticationMode
}

func NewAuthenticationProjection(
	repository domain.RepositoryIdentity,
	transport domain.GitTransport,
	mode AuthenticationMode,
) (AuthenticationProjection, error) {
	projection := AuthenticationProjection{repository: repository, transport: transport, mode: mode}
	if !projection.Valid() {
		return AuthenticationProjection{}, ErrExecutorContract
	}
	return projection, nil
}

func (p AuthenticationProjection) Repository() domain.RepositoryIdentity { return p.repository }
func (p AuthenticationProjection) Transport() domain.GitTransport        { return p.transport }
func (p AuthenticationProjection) Mode() AuthenticationMode              { return p.mode }
func (p AuthenticationProjection) Valid() bool {
	if !p.repository.Valid() || !p.transport.Valid() || !p.mode.Valid() {
		return false
	}
	switch p.mode {
	case AuthenticationAnonymousHTTPS, AuthenticationCredentialHelperHTTPS:
		return p.transport == domain.HTTPSGitTransport()
	case AuthenticationDefaultKeySSH:
		return p.transport == domain.SSHGitTransport()
	default:
		return false
	}
}

func (AuthenticationProjection) String() string   { return "<git-authentication:redacted>" }
func (AuthenticationProjection) GoString() string { return "<git-authentication:redacted>" }
func (p AuthenticationProjection) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p AuthenticationProjection) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (AuthenticationProjection) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"authentication": "redacted"})
}
