package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	AggregateOperationTimeout = 5 * time.Minute
	LocalCommandTimeout       = 15 * time.Second
	NetworkCommandTimeout     = 60 * time.Second
	TerminationGrace          = 2 * time.Second

	MaximumReferenceCount       = 1 << 15
	MaximumReferenceBytes       = 1024
	MaximumInventoryPathCount   = 1 << 14
	MaximumInventoryPathBytes   = 14 << 20
	MaximumBlobBytes            = 64 << 20
	MaximumValidatedTreeBytes   = 512 << 20
	MaximumWorkspaceBytes       = 1 << 30
	WorkspaceHeadroomBytes      = 256 << 20
	MaximumAttributeBatchPaths  = 128
	MaximumAttributeBatchBytes  = 128 << 10
	MaximumCheckoutAttributes   = 6
	MaximumCommandArguments     = 256
	MaximumCommandArgumentBytes = 4096
)

var ErrExecutorContract = errors.New("invalid Git executor contract")

// ExecutionBudget is the immutable, operation-scoped time budget. Individual
// commands consume the one aggregate deadline; they never receive fresh
// aggregate windows.
type ExecutionBudget struct {
	aggregate time.Duration
	local     time.Duration
	network   time.Duration
	grace     time.Duration
}

var defaultExecutionBudget = ExecutionBudget{
	aggregate: AggregateOperationTimeout,
	local:     LocalCommandTimeout,
	network:   NetworkCommandTimeout,
	grace:     TerminationGrace,
}

func DefaultExecutionBudget() ExecutionBudget             { return defaultExecutionBudget }
func (b ExecutionBudget) Aggregate() time.Duration        { return b.aggregate }
func (b ExecutionBudget) LocalMaximum() time.Duration     { return b.local }
func (b ExecutionBudget) NetworkMaximum() time.Duration   { return b.network }
func (b ExecutionBudget) TerminationGrace() time.Duration { return b.grace }
func (b ExecutionBudget) Valid() bool                     { return b == defaultExecutionBudget }

// WorkspaceEnforcement identifies the resource semantics. Disk
// preflight plus post-operation scans are advisory; they are not a hard quota.
type WorkspaceEnforcement string

const WorkspaceEnforcementAdvisory WorkspaceEnforcement = "advisory_postcondition"

func (e WorkspaceEnforcement) Valid() bool { return e == WorkspaceEnforcementAdvisory }

// WorkspaceBudget is the closed whole-workspace allocation and postcondition.
// It is distinct from the selected-tree validation ceiling.
type WorkspaceBudget struct {
	declared    uint64
	headroom    uint64
	enforcement WorkspaceEnforcement
}

var defaultWorkspaceBudget = WorkspaceBudget{
	declared: MaximumWorkspaceBytes, headroom: WorkspaceHeadroomBytes,
	enforcement: WorkspaceEnforcementAdvisory,
}

func DefaultWorkspaceBudget() WorkspaceBudget               { return defaultWorkspaceBudget }
func (b WorkspaceBudget) DeclaredMaximumBytes() uint64      { return b.declared }
func (b WorkspaceBudget) HeadroomBytes() uint64             { return b.headroom }
func (b WorkspaceBudget) Enforcement() WorkspaceEnforcement { return b.enforcement }
func (b WorkspaceBudget) Valid() bool                       { return b == defaultWorkspaceBudget }
func (b WorkspaceBudget) PreflightBytes() (uint64, bool) {
	if !b.Valid() || b.declared > ^uint64(0)-b.headroom {
		return 0, false
	}
	return b.declared + b.headroom, true
}
func (b WorkspaceBudget) AllowsObservedUsage(bytes uint64) bool {
	return b.Valid() && bytes <= b.declared
}
func (b WorkspaceBudget) AllowsMaterialization(currentBytes uint64, inventory TreeInventory) bool {
	return b.Valid() && inventory.Valid() && currentBytes <= b.declared &&
		inventory.TreeBytes() <= b.declared-currentBytes
}

// FailureCode is the closed, non-data-bearing executor fault taxonomy.
type FailureCode string

const (
	FailureInvalidOperation                    FailureCode = "invalid_operation"
	FailureUnsupportedRuntime                  FailureCode = "unsupported_git_runtime"
	FailureRepositoryAuthorityConflict         FailureCode = "repository_authority_conflict"
	FailureAuthenticationProjectionUnavailable FailureCode = "authentication_projection_unavailable"
	FailureTransportHelperUnavailable          FailureCode = "transport_helper_unavailable"
	FailurePolicyRejected                      FailureCode = "policy_rejected"
	FailureAccess                              FailureCode = "access_failed"
	FailureCommand                             FailureCode = "command_failed"
	FailureTimedOut                            FailureCode = "timed_out"
	FailureCancelled                           FailureCode = "cancelled"
	FailureOutputLimit                         FailureCode = "output_limit"
	FailureMalformedProtocol                   FailureCode = "malformed_protocol"
	FailureResourceLimit                       FailureCode = "resource_limit"
	FailureRepositoryConflict                  FailureCode = "repository_conflict"
	FailureReferenceNotFound                   FailureCode = "reference_not_found"
	FailureReferenceAmbiguous                  FailureCode = "reference_ambiguous"
	FailureDefaultBranchUnavailable            FailureCode = "default_branch_unavailable"
)

func (c FailureCode) Valid() bool {
	switch c {
	case FailureInvalidOperation, FailureUnsupportedRuntime, FailureRepositoryAuthorityConflict,
		FailureAuthenticationProjectionUnavailable, FailureTransportHelperUnavailable, FailurePolicyRejected,
		FailureAccess, FailureCommand, FailureTimedOut, FailureCancelled, FailureOutputLimit,
		FailureMalformedProtocol, FailureResourceLimit, FailureRepositoryConflict:
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
	OperationProbeExecPath    Operation = "probe_exec_path"
	OperationAuditConfig      Operation = "audit_config"
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
	case OperationInitialize, OperationProbeExecPath, OperationAuditConfig, OperationEnumerateRefs, OperationFetch,
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
	ConfigOutputGrammar    OutputGrammar = "config_list"
	StatusOutputGrammar    OutputGrammar = "status_porcelain_v1"
)

func (g OutputGrammar) Valid() bool {
	switch g {
	case NoOutputGrammar, RemoteOutputGrammar, ScalarOutputGrammar, TreeOutputGrammar,
		IndexOutputGrammar, AttributeOutputGrammar, ConfigOutputGrammar, StatusOutputGrammar:
		return true
	default:
		return false
	}
}

// RuntimeProfile identifies the exact source-reviewed Git implementation for
// which argv and child-process behavior are qualified.
type RuntimeProfile struct {
	id         string
	gitVersion string
	hostOS     string
	hostArch   string
	hostMajor  uint32
}

var appleGit154DarwinARM64 = RuntimeProfile{
	id: "apple_git_154_3_macos15_arm64", gitVersion: "2.39.5 (Apple Git-154.3)",
	hostOS: "darwin", hostArch: "arm64", hostMajor: 15,
}

func AppleGit154DarwinARM64Profile() RuntimeProfile { return appleGit154DarwinARM64 }
func (p RuntimeProfile) ID() string                 { return p.id }
func (p RuntimeProfile) GitVersion() string         { return p.gitVersion }
func (p RuntimeProfile) HostOS() string             { return p.hostOS }
func (p RuntimeProfile) HostArchitecture() string   { return p.hostArch }
func (p RuntimeProfile) HostMajorVersion() uint32   { return p.hostMajor }
func (p RuntimeProfile) Valid() bool                { return p == appleGit154DarwinARM64 }

// AllowedChildPurposes returns the complete image-purpose allowlist for one
// operation under this exact runtime profile. Ordering and phase correlation
// are verified by the later recorder, but an unlisted child is never allowed.
// In particular, SSH fetch-pack remains in the top-level Git image and is not
// admitted as a child.
func (p RuntimeProfile) AllowedChildPurposes(
	operation Operation,
	auth AuthenticationProjection,
) ([]RuntimeChildPurpose, error) {
	if !p.Valid() || !operation.Valid() {
		return nil, ErrExecutorContract
	}
	switch operation {
	case OperationEnumerateRefs:
		if !auth.Valid() {
			return nil, ErrExecutorContract
		}
		switch auth.mode {
		case AuthenticationAnonymousHTTPS:
			return []RuntimeChildPurpose{ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS}, nil
		case AuthenticationCredentialHelperHTTPS:
			return []RuntimeChildPurpose{
				ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildCredentialShell, ChildCredential,
			}, nil
		case AuthenticationDefaultKeySSH:
			return []RuntimeChildPurpose{ChildSSHWrapper, ChildSSHClient}, nil
		}
	case OperationFetch:
		if !auth.Valid() {
			return nil, ErrExecutorContract
		}
		switch auth.mode {
		case AuthenticationAnonymousHTTPS:
			return []RuntimeChildPurpose{
				ChildGitRevList, ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack, ChildGitIndexPack,
			}, nil
		case AuthenticationCredentialHelperHTTPS:
			return []RuntimeChildPurpose{
				ChildGitRevList, ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack,
				ChildGitIndexPack, ChildCredentialShell, ChildCredential,
			}, nil
		case AuthenticationDefaultKeySSH:
			return []RuntimeChildPurpose{ChildGitRevList, ChildSSHWrapper, ChildSSHClient, ChildGitIndexPack}, nil
		}
	default:
		if auth != (AuthenticationProjection{}) {
			return nil, ErrExecutorContract
		}
		return []RuntimeChildPurpose{}, nil
	}
	return nil, ErrExecutorContract
}

// RuntimeChildPurpose is a closed semantic binding. It never carries an
// executable locator or caller-supplied environment value.
type RuntimeChildPurpose string

const (
	ChildGitRemoteHTTPSDriver RuntimeChildPurpose = "git_remote_https_driver"
	ChildRemoteHTTPS          RuntimeChildPurpose = "git_remote_https"
	ChildGitFetchPack         RuntimeChildPurpose = "git_fetch_pack"
	ChildGitIndexPack         RuntimeChildPurpose = "git_index_pack"
	ChildGitRevList           RuntimeChildPurpose = "git_rev_list"
	ChildCredentialShell      RuntimeChildPurpose = "git_credential_shell"
	ChildCredential           RuntimeChildPurpose = "git_https_credential_helper"
	ChildSSHWrapper           RuntimeChildPurpose = "ai4j_git_ssh_wrapper"
	ChildSSHClient            RuntimeChildPurpose = "git_ssh_native_client"
)

func (p RuntimeChildPurpose) Valid() bool {
	switch p {
	case ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack, ChildGitIndexPack,
		ChildGitRevList, ChildCredentialShell, ChildCredential, ChildSSHWrapper, ChildSSHClient:
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

var gitHardenedEnvironmentProfile = mustEnvironmentProfile("git_hardened")

func GitHardenedEnvironmentProfile() lifecycle.ProcessEnvironmentProfileID {
	return gitHardenedEnvironmentProfile
}

func GitHardenedEnvironment() []lifecycle.EnvironmentBinding {
	return []lifecycle.EnvironmentBinding{
		{Name: "GIT_ATTR_NOSYSTEM", Value: "1"},
		{Name: "GIT_CONFIG_GLOBAL", Value: "/dev/null"},
		{Name: "GIT_CONFIG_NOSYSTEM", Value: "1"},
		{Name: "GIT_LFS_SKIP_SMUDGE", Value: "1"},
		{Name: "GIT_OPTIONAL_LOCKS", Value: "0"},
		{Name: "GIT_PROTOCOL_FROM_USER", Value: "0"},
		{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
		{Name: "LANG", Value: "C"},
		{Name: "LC_ALL", Value: "C"},
		{Name: "PATH", Value: "/dev/null"},
	}
}

func mustEnvironmentProfile(value string) lifecycle.ProcessEnvironmentProfileID {
	profile, err := lifecycle.NewProcessEnvironmentProfileID(value)
	if err != nil {
		panic("invalid built-in Git environment profile")
	}
	return profile
}
