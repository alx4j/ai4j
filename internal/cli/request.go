package cli

import (
	"fmt"

	"github.com/alx4j/ai4j/internal/domain"
)

type Request interface {
	Command() Command
	OutputMode() OutputMode
	request()
}

// UnsupportedRequest is a fully validated command that cannot be completed
// through the documented target or host capabilities. Dispatch returns
// unsupported_capability before source acquisition or mutation.
type UnsupportedRequest struct {
	command Command
	output  OutputMode
	message string
}

func (UnsupportedRequest) request()                 {}
func (r UnsupportedRequest) Command() Command       { return r.command }
func (r UnsupportedRequest) OutputMode() OutputMode { return r.output }
func (r UnsupportedRequest) Message() string        { return r.message }

type SourceOptions struct {
	repository         string
	repositoryProvided bool
	reference          string
	referenceProvided  bool
	checkout           string
	checkoutProvided   bool
	allowDirty         bool
}

func NewSourceOptions(repository string, repositoryProvided bool, reference string, referenceProvided bool) (SourceOptions, error) {
	if (!repositoryProvided && repository != "") || (repositoryProvided && repository == "") ||
		(!referenceProvided && reference != "") || (referenceProvided && reference == "") ||
		len(repository) > 512 || len(reference) > 512 {
		return SourceOptions{}, fmt.Errorf("source options are contradictory")
	}
	return SourceOptions{repository: repository, repositoryProvided: repositoryProvided, reference: reference, referenceProvided: referenceProvided}, nil
}

func (o SourceOptions) Repository() string  { return o.repository }
func (o SourceOptions) HasRepository() bool { return o.repositoryProvided }
func (o SourceOptions) Reference() string   { return o.reference }
func (o SourceOptions) HasReference() bool  { return o.referenceProvided }
func (o SourceOptions) Checkout() string    { return o.checkout }
func (o SourceOptions) HasCheckout() bool   { return o.checkoutProvided }
func (o SourceOptions) AllowDirty() bool    { return o.allowDirty }

func NewDevelopmentSourceOptions(checkout string, allowDirty bool) (SourceOptions, error) {
	if checkout == "" || len(checkout) > 4096 {
		return SourceOptions{}, fmt.Errorf("development source options are contradictory")
	}
	return SourceOptions{checkout: checkout, checkoutProvided: true, allowDirty: allowDirty}, nil
}

type ValidateRequest struct {
	source SourceOptions
	output OutputMode
}

func (ValidateRequest) request()                 {}
func (ValidateRequest) Command() Command         { return CommandValidate }
func (r ValidateRequest) OutputMode() OutputMode { return r.output }
func (r ValidateRequest) Source() SourceOptions  { return r.source }

type InitRequest struct {
	targets  []BuildTarget
	output   string
	examples bool
	mode     OutputMode
}

func (InitRequest) request()                 {}
func (InitRequest) Command() Command         { return CommandInit }
func (r InitRequest) OutputMode() OutputMode { return r.mode }
func (r InitRequest) Targets() []BuildTarget { return append([]BuildTarget(nil), r.targets...) }
func (r InitRequest) Output() string         { return r.output }
func (r InitRequest) Examples() bool         { return r.examples }

type BuildTarget string

const (
	BuildTargetClaude BuildTarget = "claude"
	BuildTargetCodex  BuildTarget = "codex"
)

func (t BuildTarget) Valid() bool { return t == BuildTargetClaude || t == BuildTargetCodex }

type BuildHost string

const (
	BuildHostDarwinARM64  BuildHost = "darwin-arm64"
	BuildHostWindowsAMD64 BuildHost = "windows-amd64"
)

func (h BuildHost) Valid() bool { return h == BuildHostDarwinARM64 || h == BuildHostWindowsAMD64 }

type BuildRequest struct {
	source  SourceOptions
	target  BuildTarget
	host    BuildHost
	output  string
	all     bool
	assets  []string
	bundles []string
	mode    OutputMode
}

func (BuildRequest) request()                 {}
func (BuildRequest) Command() Command         { return CommandBuild }
func (r BuildRequest) OutputMode() OutputMode { return r.mode }
func (r BuildRequest) Source() SourceOptions  { return r.source }
func (r BuildRequest) Target() BuildTarget    { return r.target }
func (r BuildRequest) Host() BuildHost        { return r.host }
func (r BuildRequest) Output() string         { return r.output }
func (r BuildRequest) SelectAll() bool        { return r.all }
func (r BuildRequest) Assets() []string       { return append([]string(nil), r.assets...) }
func (r BuildRequest) Bundles() []string      { return append([]string(nil), r.bundles...) }

type SelectionOptions struct {
	all     bool
	assets  []string
	bundles []string
}

func NewSelectionOptions(all bool, assets, bundles []string) SelectionOptions {
	return SelectionOptions{all: all, assets: append([]string(nil), assets...), bundles: append([]string(nil), bundles...)}
}

func (s SelectionOptions) SelectAll() bool   { return s.all }
func (s SelectionOptions) Assets() []string  { return append([]string(nil), s.assets...) }
func (s SelectionOptions) Bundles() []string { return append([]string(nil), s.bundles...) }

type ConflictPolicy string

const (
	ConflictFail         ConflictPolicy = "fail"
	ConflictKeep         ConflictPolicy = "keep"
	ConflictReplaceOwned ConflictPolicy = "replace-owned"
	ConflictInteractive  ConflictPolicy = "interactive"
)

func (p ConflictPolicy) Valid() bool {
	return p == ConflictFail || p == ConflictKeep || p == ConflictReplaceOwned || p == ConflictInteractive
}

type InstallRequest struct {
	source            SourceOptions
	target            BuildTarget
	scope             Scope
	project           string
	hasProject        bool
	selection         SelectionOptions
	installation      domain.InstallationID
	hasInstallation   bool
	v1                bool
	expectedCommit    domain.CommitOID
	hasExpected       bool
	expectedDigest    string
	hasExpectedDigest bool
	dryRun            bool
	yes               bool
	output            OutputMode
}

func (InstallRequest) request()                                {}
func (InstallRequest) Command() Command                        { return CommandInstall }
func (r InstallRequest) OutputMode() OutputMode                { return r.output }
func (r InstallRequest) Source() SourceOptions                 { return r.source }
func (r InstallRequest) Target() BuildTarget                   { return r.target }
func (r InstallRequest) Scope() Scope                          { return r.scope }
func (r InstallRequest) Project() (string, bool)               { return r.project, r.hasProject }
func (r InstallRequest) Selection() SelectionOptions           { return r.selection }
func (r InstallRequest) InstallationID() domain.InstallationID { return r.installation }
func (r InstallRequest) HasInstallationID() bool               { return r.hasInstallation }
func (r InstallRequest) V1() bool                              { return r.v1 }
func (r InstallRequest) ExpectedCommit() (domain.CommitOID, bool) {
	return r.expectedCommit, r.hasExpected
}
func (r InstallRequest) ExpectedSourceDigest() (string, bool) {
	return r.expectedDigest, r.hasExpectedDigest
}
func (r InstallRequest) DryRun() bool   { return r.dryRun }
func (r InstallRequest) Approved() bool { return r.yes }

type UpdateRequest struct {
	installation      domain.InstallationID
	hasInstallation   bool
	source            SourceOptions
	policy            ConflictPolicy
	v1                bool
	expectedCommit    domain.CommitOID
	hasExpected       bool
	expectedDigest    string
	hasExpectedDigest bool
	dryRun            bool
	yes               bool
	output            OutputMode
}

func (UpdateRequest) request()                                {}
func (UpdateRequest) Command() Command                        { return CommandUpdate }
func (r UpdateRequest) OutputMode() OutputMode                { return r.output }
func (r UpdateRequest) InstallationID() domain.InstallationID { return r.installation }
func (r UpdateRequest) HasInstallationID() bool               { return r.hasInstallation }
func (r UpdateRequest) Source() SourceOptions                 { return r.source }
func (r UpdateRequest) ConflictPolicy() ConflictPolicy        { return r.policy }
func (r UpdateRequest) V1() bool                              { return r.v1 }
func (r UpdateRequest) ExpectedCommit() (domain.CommitOID, bool) {
	return r.expectedCommit, r.hasExpected
}
func (r UpdateRequest) ExpectedSourceDigest() (string, bool) {
	return r.expectedDigest, r.hasExpectedDigest
}
func (r UpdateRequest) DryRun() bool   { return r.dryRun }
func (r UpdateRequest) Approved() bool { return r.yes }

type SyncRequest struct {
	installation      domain.InstallationID
	selection         SelectionOptions
	allowDirty        bool
	expectedDigest    string
	hasExpectedDigest bool
	policy            ConflictPolicy
	dryRun            bool
	yes               bool
	output            OutputMode
}

func (SyncRequest) request()                                {}
func (SyncRequest) Command() Command                        { return CommandSync }
func (r SyncRequest) OutputMode() OutputMode                { return r.output }
func (r SyncRequest) InstallationID() domain.InstallationID { return r.installation }
func (r SyncRequest) Selection() SelectionOptions           { return r.selection }
func (r SyncRequest) ConflictPolicy() ConflictPolicy        { return r.policy }
func (r SyncRequest) AllowDirty() bool                      { return r.allowDirty }
func (r SyncRequest) ExpectedSourceDigest() (string, bool) {
	return r.expectedDigest, r.hasExpectedDigest
}
func (r SyncRequest) DryRun() bool   { return r.dryRun }
func (r SyncRequest) Approved() bool { return r.yes }

type Scope string

const (
	ScopeUser          Scope = "user"
	ScopeProjectLocal  Scope = "project-local"
	ScopeProjectShared Scope = "project-shared"
)

func (s Scope) Valid() bool {
	return s == ScopeUser || s == ScopeProjectLocal || s == ScopeProjectShared
}

type ListRequest struct {
	target    BuildTarget
	hasTarget bool
	scope     Scope
	hasScope  bool
	output    OutputMode
}

func (ListRequest) request()                 {}
func (ListRequest) Command() Command         { return CommandList }
func (r ListRequest) OutputMode() OutputMode { return r.output }
func (r ListRequest) Target() BuildTarget    { return r.target }
func (r ListRequest) HasTarget() bool        { return r.hasTarget }
func (r ListRequest) Scope() Scope           { return r.scope }
func (r ListRequest) HasScope() bool         { return r.hasScope }

type StatusRequest struct {
	installation    domain.InstallationID
	hasInstallation bool
	checkUpdates    bool
	output          OutputMode
}

func (StatusRequest) request()                                {}
func (StatusRequest) Command() Command                        { return CommandStatus }
func (r StatusRequest) OutputMode() OutputMode                { return r.output }
func (r StatusRequest) CheckUpdates() bool                    { return r.checkUpdates }
func (r StatusRequest) InstallationID() domain.InstallationID { return r.installation }
func (r StatusRequest) HasInstallationID() bool               { return r.hasInstallation }

type DoctorRequest struct {
	installation domain.InstallationID
	testMCP      string
	yes          bool
	output       OutputMode
}

func (DoctorRequest) request()                                {}
func (DoctorRequest) Command() Command                        { return CommandDoctor }
func (r DoctorRequest) OutputMode() OutputMode                { return r.output }
func (r DoctorRequest) InstallationID() domain.InstallationID { return r.installation }
func (r DoctorRequest) TestMCP() string                       { return r.testMCP }
func (r DoctorRequest) HasMCPTest() bool                      { return r.testMCP != "" }
func (r DoctorRequest) Approved() bool                        { return r.yes }

type RollbackRequest struct {
	installation domain.InstallationID
	operation    domain.OperationID
	hasOperation bool
	policy       ConflictPolicy
	dryRun       bool
	yes          bool
	output       OutputMode
}

func (RollbackRequest) request()                                {}
func (RollbackRequest) Command() Command                        { return CommandRollback }
func (r RollbackRequest) OutputMode() OutputMode                { return r.output }
func (r RollbackRequest) InstallationID() domain.InstallationID { return r.installation }
func (r RollbackRequest) OperationID() domain.OperationID       { return r.operation }
func (r RollbackRequest) HasOperationID() bool                  { return r.hasOperation }
func (r RollbackRequest) ConflictPolicy() ConflictPolicy        { return r.policy }
func (r RollbackRequest) DryRun() bool                          { return r.dryRun }
func (r RollbackRequest) Approved() bool                        { return r.yes }

type UninstallRequest struct {
	installation    domain.InstallationID
	hasInstallation bool
	policy          ConflictPolicy
	v1              bool
	dryRun          bool
	yes             bool
	output          OutputMode
}

func (UninstallRequest) request()                                {}
func (UninstallRequest) Command() Command                        { return CommandUninstall }
func (r UninstallRequest) OutputMode() OutputMode                { return r.output }
func (r UninstallRequest) Approved() bool                        { return r.yes }
func (r UninstallRequest) InstallationID() domain.InstallationID { return r.installation }
func (r UninstallRequest) HasInstallationID() bool               { return r.hasInstallation }
func (r UninstallRequest) ConflictPolicy() ConflictPolicy        { return r.policy }
func (r UninstallRequest) V1() bool                              { return r.v1 }
func (r UninstallRequest) DryRun() bool                          { return r.dryRun }

type HistoryRequest struct {
	installation domain.InstallationID
	output       OutputMode
}

func (HistoryRequest) request()                                {}
func (HistoryRequest) Command() Command                        { return CommandHistory }
func (r HistoryRequest) OutputMode() OutputMode                { return r.output }
func (r HistoryRequest) InstallationID() domain.InstallationID { return r.installation }

type HistoryPurgeSelection string

const (
	HistoryPurgeOperation HistoryPurgeSelection = "operation"
	HistoryPurgeExpired   HistoryPurgeSelection = "expired"
	HistoryPurgeAll       HistoryPurgeSelection = "all"
)

type HistoryPurgeRequest struct {
	installation domain.InstallationID
	selection    HistoryPurgeSelection
	operation    domain.OperationID
	dryRun       bool
	yes          bool
	output       OutputMode
}

func (HistoryPurgeRequest) request()                                {}
func (HistoryPurgeRequest) Command() Command                        { return CommandHistoryPurge }
func (r HistoryPurgeRequest) OutputMode() OutputMode                { return r.output }
func (r HistoryPurgeRequest) InstallationID() domain.InstallationID { return r.installation }
func (r HistoryPurgeRequest) Selection() HistoryPurgeSelection      { return r.selection }
func (r HistoryPurgeRequest) OperationID() domain.OperationID       { return r.operation }
func (r HistoryPurgeRequest) DryRun() bool                          { return r.dryRun }
func (r HistoryPurgeRequest) Approved() bool                        { return r.yes }

type VersionRequest struct{ output OutputMode }

func (VersionRequest) request()                 {}
func (VersionRequest) Command() Command         { return CommandVersion }
func (r VersionRequest) OutputMode() OutputMode { return r.output }
