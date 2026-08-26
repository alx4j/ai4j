package cli

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
)

type Operation string

const (
	OperationInstall      Operation = "install"
	OperationUpdate       Operation = "update"
	OperationSync         Operation = "sync"
	OperationRollback     Operation = "rollback"
	OperationUninstall    Operation = "uninstall"
	OperationHistoryPurge Operation = "history_purge"
)

func (o Operation) Valid() bool {
	return o == OperationInstall || o == OperationUpdate || o == OperationSync || o == OperationRollback || o == OperationUninstall || o == OperationHistoryPurge
}
func (o Operation) String() string { return string(o) }
func (o Operation) command() Command {
	switch o {
	case OperationInstall:
		return CommandInstall
	case OperationUpdate:
		return CommandUpdate
	case OperationSync:
		return CommandSync
	case OperationRollback:
		return CommandRollback
	case OperationUninstall:
		return CommandUninstall
	case OperationHistoryPurge:
		return CommandHistoryPurge
	default:
		return ""
	}
}

type RefKind = gitsource.ResolvedReferenceKind

const (
	RefDefaultBranch = gitsource.ResolvedDefaultBranch
	RefBranch        = gitsource.ResolvedBranch
	RefTag           = gitsource.ResolvedTag
	RefCommit        = gitsource.ResolvedCommit
)

type SourceMode string

const (
	SourceGitHub      SourceMode = "github"
	SourceDevelopment SourceMode = "development_source"
)

type Source struct {
	provenance     gitsource.RenderedProvenance
	mode           SourceMode
	checkout       string
	sourceDigest   domain.RenderedDigest
	renderedDigest domain.RenderedDigest
	buildCommit    domain.BuildCommit
	dirty          bool
}

func NewSource(provenance gitsource.RenderedProvenance) (Source, error) {
	if !provenance.Valid() {
		return Source{}, fmt.Errorf("source provenance is invalid")
	}
	return Source{provenance: provenance, mode: SourceGitHub}, nil
}

func NewDevelopmentSource(checkout string, sourceDigest, renderedDigest domain.RenderedDigest, buildCommit domain.BuildCommit, dirty bool) (Source, error) {
	if !filepath.IsAbs(checkout) || !sourceDigest.Valid() || !renderedDigest.Valid() || !buildCommit.Valid() {
		return Source{}, fmt.Errorf("development source provenance is invalid")
	}
	return Source{mode: SourceDevelopment, checkout: filepath.Clean(checkout), sourceDigest: sourceDigest, renderedDigest: renderedDigest, buildCommit: buildCommit, dirty: dirty}, nil
}

func (s Source) Mode() SourceMode                    { return s.mode }
func (s Source) Checkout() string                    { return s.checkout }
func (s Source) SourceDigest() domain.RenderedDigest { return s.sourceDigest }
func (s Source) Dirty() bool                         { return s.dirty }

func (s Source) Selection() domain.SourceSelection {
	if s.mode == SourceDevelopment {
		return domain.ExplicitSource()
	}
	return s.provenance.Source().SourceSelection()
}
func (s Source) Repository() domain.RepositoryIdentity {
	return s.provenance.Source().Repository()
}
func (s Source) RequestedRef() string {
	value, _ := s.provenance.Source().RequestedReference().Value()
	return value
}
func (s Source) HasRequestedRef() bool {
	_, provided := s.provenance.Source().RequestedReference().Value()
	return provided
}
func (s Source) ResolvedRefKind() RefKind {
	return s.provenance.Source().ResolvedReference().Kind()
}
func (s Source) ResolvedRefName() string {
	return s.provenance.Source().ResolvedReference().Name()
}
func (s Source) Commit() domain.CommitIdentity { return s.provenance.Source().Commit() }
func (s Source) RootTree() domain.TreeOID      { return s.provenance.Source().RootTree() }
func (s Source) TrackingPolicy() gitsource.TrackingPolicy {
	return s.provenance.Source().TrackingPolicy()
}
func (s Source) RenderedDigest() domain.RenderedDigest {
	if s.mode == SourceDevelopment {
		return s.renderedDigest
	}
	return s.provenance.RenderedDigest()
}
func (s Source) CLIBuildCommit() domain.BuildCommit {
	if s.mode == SourceDevelopment {
		return s.buildCommit
	}
	return s.provenance.BuildCommit()
}
func (s Source) Valid() bool { return s.valid() }
func (s Source) valid() bool {
	if s.mode == SourceGitHub {
		return s.provenance.Valid()
	}
	return s.mode == SourceDevelopment && filepath.IsAbs(s.checkout) && s.sourceDigest.Valid() && s.renderedDigest.Valid() && s.buildCommit.Valid()
}

type ContentChange string

const (
	ContentUnchanged ContentChange = "unchanged"
	ContentAdded     ContentChange = "added"
	ContentChanged   ContentChange = "changed"
	ContentRemoved   ContentChange = "removed"
)

func (c ContentChange) valid() bool {
	return c == ContentUnchanged || c == ContentAdded || c == ContentChanged || c == ContentRemoved
}

var (
	contentIdentifierPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	sha256Pattern               = regexp.MustCompile(`^[0-9a-f]{64}$`)
	environmentNamePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	conflictCodePattern         = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	hyphenatedIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)
	pluginIdentifierPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	doctorCheckIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	goVersionPattern            = regexp.MustCompile(`^go[0-9]+\.[0-9]+(\.[0-9]+)?([a-z0-9.-]+)?$`)
	goTargetPattern             = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

type ComponentType string

const (
	ComponentSkill             ComponentType = "skill"
	ComponentAgent             ComponentType = "agent"
	ComponentSharedInstruction ComponentType = "shared_instruction"
	ComponentScript            ComponentType = "script"
	ComponentBinary            ComponentType = "binary"
	ComponentMCP               ComponentType = "mcp"
	ComponentPromptTemplate    ComponentType = "prompt_template"
	ComponentReference         ComponentType = "reference"
	ComponentSupport           ComponentType = "support"
	ComponentHook              ComponentType = "hook"
	ComponentExtension         ComponentType = "extension"
)

func validComponentType(value ComponentType) bool {
	switch value {
	case ComponentSkill, ComponentAgent, ComponentSharedInstruction, ComponentScript, ComponentBinary, ComponentMCP,
		ComponentPromptTemplate, ComponentReference, ComponentSupport, ComponentHook, ComponentExtension:
		return true
	default:
		return false
	}
}

type ContentItem struct {
	componentType                    ComponentType
	identifier, sourcePath, checksum string
	change                           ContentChange
	execution                        *Execution
}

func NewContentItem(componentType ComponentType, identifier, sourcePath, checksum string, change ContentChange, execution *Execution) (ContentItem, error) {
	cleanPath := path.Clean(sourcePath)
	if !validComponentType(componentType) || !contentIdentifierPattern.MatchString(identifier) || !boundedText(sourcePath, 1024, false) || cleanPath != sourcePath || cleanPath == "." || strings.HasPrefix(cleanPath, "../") || strings.HasPrefix(cleanPath, "/") || !validSHA256(checksum) || !change.valid() {
		return ContentItem{}, fmt.Errorf("active-content item is incomplete")
	}
	wantsExecution := componentType == ComponentScript || componentType == ComponentBinary || componentType == ComponentMCP
	if wantsExecution != (execution != nil) {
		return ContentItem{}, fmt.Errorf("component execution disclosure is inconsistent")
	}
	var owned *Execution
	if execution != nil {
		copy := execution.clone()
		if !copy.valid() {
			return ContentItem{}, fmt.Errorf("execution disclosure is invalid")
		}
		owned = &copy
	}
	return ContentItem{componentType: componentType, identifier: identifier, sourcePath: sourcePath, checksum: checksum, change: change, execution: owned}, nil
}
func (v ContentItem) ComponentType() ComponentType { return v.componentType }
func (v ContentItem) Identifier() string           { return v.identifier }
func (v ContentItem) SourcePath() string           { return v.sourcePath }
func (v ContentItem) Checksum() string             { return v.checksum }
func (v ContentItem) Change() ContentChange        { return v.change }
func (v ContentItem) Execution() (Execution, bool) {
	if v.execution == nil {
		return Execution{}, false
	}
	return v.execution.clone(), true
}

type ExecutionOwnership string

const (
	ExecutionToolkitOwned ExecutionOwnership = "toolkit_owned"
	ExecutionHostResolved ExecutionOwnership = "host_resolved"
)

type ExecutionDependency string

const (
	DependencyRequired ExecutionDependency = "required"
	DependencyOptional ExecutionDependency = "optional"
)

type Placeholder string

const (
	PlaceholderPluginRoot Placeholder = "${CLAUDE_PLUGIN_ROOT}"
	PlaceholderProjectDir Placeholder = "${CLAUDE_PROJECT_DIR}"
)

type Execution struct {
	ownership    ExecutionOwnership
	dependency   ExecutionDependency
	command, cwd string
	args         []string
	placeholders []Placeholder
	environment  []string
}

func NewExecution(ownership ExecutionOwnership, dependency ExecutionDependency, command string, args []string, cwd string, placeholders []Placeholder, environment []string) (Execution, error) {
	if len(args) > 128 || len(placeholders) > 2 || len(environment) > 128 || hasDuplicatePlaceholders(placeholders) || hasDuplicateStrings(environment) {
		return Execution{}, fmt.Errorf("execution disclosure exceeds collection bounds or contains duplicates")
	}
	value := Execution{ownership: ownership, dependency: dependency, command: command, args: append([]string(nil), args...), cwd: cwd, placeholders: uniqueSortedPlaceholders(placeholders), environment: uniqueSortedStrings(environment)}
	if !value.valid() {
		return Execution{}, fmt.Errorf("execution disclosure is invalid")
	}
	return value, nil
}
func (e Execution) Ownership() ExecutionOwnership   { return e.ownership }
func (e Execution) Dependency() ExecutionDependency { return e.dependency }
func (e Execution) Command() string                 { return e.command }
func (e Execution) Args() []string                  { return cloneStrings(e.args) }
func (e Execution) CWD() string                     { return e.cwd }
func (e Execution) SupportedPlaceholders() []Placeholder {
	return clonePlaceholders(e.placeholders)
}
func (e Execution) Environment() []string { return cloneStrings(e.environment) }
func (e Execution) clone() Execution {
	e.args = append([]string(nil), e.args...)
	e.placeholders = append([]Placeholder(nil), e.placeholders...)
	e.environment = append([]string(nil), e.environment...)
	return e
}
func (e Execution) valid() bool {
	if (e.ownership != ExecutionToolkitOwned && e.ownership != ExecutionHostResolved) || (e.dependency != DependencyRequired && e.dependency != DependencyOptional) || !boundedText(e.command, 1024, false) || (e.cwd != "" && !boundedText(e.cwd, 1024, false)) || len(e.args) > 128 || len(e.placeholders) > 2 || len(e.environment) > 128 {
		return false
	}
	for _, value := range e.args {
		if !boundedText(value, 4096, true) {
			return false
		}
	}
	for index, value := range e.placeholders {
		if (value != PlaceholderPluginRoot && value != PlaceholderProjectDir) || (index > 0 && e.placeholders[index-1] >= value) {
			return false
		}
	}
	for index, value := range e.environment {
		if len(value) > 128 || !environmentNamePattern.MatchString(value) || (index > 0 && e.environment[index-1] >= value) {
			return false
		}
	}
	return true
}

type ActionOwner string

const (
	ActionOwnerAI4J   ActionOwner = "ai4j"
	ActionOwnerClaude ActionOwner = "claude"
)

type ActionKind string

const (
	ActionValidateSource      ActionKind = "validate_source"
	ActionPrepareRecovery     ActionKind = "prepare_recovery"
	ActionWriteCatalog        ActionKind = "write_catalog"
	ActionRemoveCatalog       ActionKind = "remove_catalog"
	ActionRegisterMarketplace ActionKind = "register_marketplace"
	ActionRefreshMarketplace  ActionKind = "refresh_marketplace"
	ActionRemoveMarketplace   ActionKind = "remove_marketplace"
	ActionInstallPlugin       ActionKind = "install_plugin"
	ActionUpdatePlugin        ActionKind = "update_plugin"
	ActionEnablePlugin        ActionKind = "enable_plugin"
	ActionDisablePlugin       ActionKind = "disable_plugin"
	ActionUninstallPlugin     ActionKind = "uninstall_plugin"
	ActionWriteRules          ActionKind = "write_rules"
	ActionRemoveRules         ActionKind = "remove_rules"
	ActionCommitState         ActionKind = "commit_state"
	ActionRemoveState         ActionKind = "remove_state"
	ActionCleanup             ActionKind = "cleanup"
)

type RecoveryRequirement string

const (
	RecoveryNone              RecoveryRequirement = "none"
	RecoveryStructuralInverse RecoveryRequirement = "structural_inverse"
	RecoveryFullPreimage      RecoveryRequirement = "full_preimage"
	RecoveryNativeArtifact    RecoveryRequirement = "native_artifact"
	RecoveryExactHandle       RecoveryRequirement = "exact_handle"
)

type Action struct {
	sequence                                    int
	owner                                       ActionOwner
	kind                                        ActionKind
	resource                                    string
	recoveryRequirement                         RecoveryRequirement
	expectedPrecondition, proposedPostcondition Condition
}

func NewAction(sequence int, owner ActionOwner, kind ActionKind, resource string, expectedPrecondition, proposedPostcondition Condition, recoveryRequirement RecoveryRequirement) (Action, error) {
	if sequence < 1 || !validActionOwner(owner) || !validActionKind(kind) || !boundedText(resource, 256, false) || !expectedPrecondition.valid() || !proposedPostcondition.valid() || !validRecoveryRequirement(recoveryRequirement) {
		return Action{}, fmt.Errorf("action is incomplete")
	}
	return Action{sequence: sequence, owner: owner, kind: kind, resource: resource, expectedPrecondition: expectedPrecondition, proposedPostcondition: proposedPostcondition, recoveryRequirement: recoveryRequirement}, nil
}

func validActionOwner(value ActionOwner) bool {
	return value == ActionOwnerAI4J || value == ActionOwnerClaude
}
func validActionKind(value ActionKind) bool {
	switch value {
	case "validate_source", "prepare_recovery", "write_catalog", "remove_catalog", "register_marketplace", "refresh_marketplace", "remove_marketplace", "install_plugin", "update_plugin", "enable_plugin", "disable_plugin", "uninstall_plugin", "write_rules", "remove_rules", "commit_state", "remove_state", "cleanup":
		return true
	default:
		return false
	}
}
func (a Action) Sequence() int                            { return a.sequence }
func (a Action) Owner() ActionOwner                       { return a.owner }
func (a Action) Kind() ActionKind                         { return a.kind }
func (a Action) Resource() string                         { return a.resource }
func (a Action) ExpectedPrecondition() Condition          { return a.expectedPrecondition }
func (a Action) ProposedPostcondition() Condition         { return a.proposedPostcondition }
func (a Action) RecoveryRequirement() RecoveryRequirement { return a.recoveryRequirement }

func validRecoveryRequirement(value RecoveryRequirement) bool {
	switch value {
	case RecoveryNone, RecoveryStructuralInverse, RecoveryFullPreimage, RecoveryNativeArtifact, RecoveryExactHandle:
		return true
	default:
		return false
	}
}

type ConditionState string

const (
	ConditionAbsent          ConditionState = "absent"
	ConditionPresent         ConditionState = "present"
	ConditionMatchesChecksum ConditionState = "matches_checksum"
)

type Condition struct {
	state    ConditionState
	checksum string
}

func NewCondition(state ConditionState, checksum string) (Condition, error) {
	value := Condition{state: state, checksum: checksum}
	if !value.valid() {
		return Condition{}, fmt.Errorf("resource condition is invalid")
	}
	return value, nil
}
func (c Condition) State() ConditionState { return c.state }
func (c Condition) Checksum() string      { return c.checksum }
func (c Condition) HasChecksum() bool     { return c.checksum != "" }
func (c Condition) valid() bool {
	switch c.state {
	case ConditionMatchesChecksum:
		return validSHA256(c.checksum)
	case ConditionAbsent, ConditionPresent:
		return c.checksum == ""
	default:
		return false
	}
}

type Conflict struct{ code, resource, message string }

func NewConflict(code, resource, message string) (Conflict, error) {
	if !conflictCodePattern.MatchString(code) || !boundedText(resource, 256, false) || !boundedText(message, 512, false) {
		return Conflict{}, fmt.Errorf("conflict is incomplete")
	}
	return Conflict{code: code, resource: resource, message: message}, nil
}
func (c Conflict) Code() string     { return c.code }
func (c Conflict) Resource() string { return c.resource }
func (c Conflict) Message() string  { return c.message }

type StatePresence string

const (
	StateAbsent  StatePresence = "absent"
	StatePresent StatePresence = "present"
)

func (s StatePresence) valid() bool { return s == StateAbsent || s == StatePresent }

type FinalState struct{ installation, native, owned StatePresence }

func NewFinalState(installation, native, owned StatePresence) (FinalState, error) {
	if !installation.valid() || !native.valid() || !owned.valid() {
		return FinalState{}, fmt.Errorf("final state contains an unknown value")
	}
	return FinalState{installation: installation, native: native, owned: owned}, nil
}
func (s FinalState) Installation() StatePresence { return s.installation }
func (s FinalState) Native() StatePresence       { return s.native }
func (s FinalState) OwnedState() StatePresence   { return s.owned }

type UsageData struct {
	issue   UsageIssue
	option  string
	command Command
}

func NewUsageData(issue UsageIssue, option string) (UsageData, error) {
	return NewDetailedUsageData(issue, option, "")
}

func NewDetailedUsageData(issue UsageIssue, option string, command Command) (UsageData, error) {
	value := UsageData{issue: issue, option: option, command: command}
	if !value.valid() {
		return UsageData{}, fmt.Errorf("usage data is invalid")
	}
	return value, nil
}
func (UsageData) cliData()                   {}
func (d UsageData) Issue() UsageIssue        { return d.issue }
func (d UsageData) Option() string           { return d.option }
func (d UsageData) Command() (Command, bool) { return d.command, d.command.Valid() }
func (d UsageData) valid() bool {
	if d.command != "" && !d.command.Valid() {
		return false
	}
	if d.option != "" && !isKnownOption(d.option) {
		return false
	}
	optionIssue := d.issue == UsageInapplicableOption || d.issue == UsageDuplicateOption || d.issue == UsageMissingOptionValue || d.issue == UsageEmptyOptionValue || d.issue == UsageUnexpectedOptionValue || d.issue == UsageInvalidOptionValue
	if d.option != "" && !optionIssue {
		return false
	}
	switch d.issue {
	case UsageMissingExecutable, UsageAlternateExecutable, UsageMissingCommand, UsageUnknownCommand, UsageUnexpectedArgument, UsageUnknownOption, UsageMisplacedOption, UsageInapplicableOption, UsageDuplicateOption, UsageMissingOptionValue, UsageEmptyOptionValue, UsageUnexpectedOptionValue, UsageInvalidOptionValue:
		return true
	default:
		return false
	}
}

type ValidateData struct {
	source                   Source
	validationValid          bool
	errorCount, warningCount int
	content                  []ContentItem
}

func NewValidateData(source Source, valid bool, errorCount, warningCount int, content []ContentItem) (ValidateData, error) {
	if !source.valid() || errorCount < 0 || warningCount < 0 || valid != (errorCount == 0) || !validContent(content) {
		return ValidateData{}, fmt.Errorf("validation data is contradictory")
	}
	return ValidateData{source: source, validationValid: valid, errorCount: errorCount, warningCount: warningCount, content: sortedContent(content)}, nil
}
func (ValidateData) cliData()                       {}
func (d ValidateData) Source() Source               { return d.source }
func (d ValidateData) ValidationValid() bool        { return d.validationValid }
func (d ValidateData) ErrorCount() int              { return d.errorCount }
func (d ValidateData) WarningCount() int            { return d.warningCount }
func (d ValidateData) ActiveContent() []ContentItem { return append([]ContentItem(nil), d.content...) }
func (d ValidateData) valid() bool {
	if !d.source.valid() || d.errorCount < 0 || d.warningCount < 0 || (d.validationValid && d.errorCount != 0) {
		return false
	}
	for _, item := range d.content {
		if !item.valid() {
			return false
		}
	}
	return true
}

type BuildArtifact struct {
	path      string
	checksum  string
	sizeBytes uint64
}

func NewBuildArtifact(artifactPath, checksum string, sizeBytes uint64) (BuildArtifact, error) {
	clean := path.Clean(artifactPath)
	if clean != artifactPath || clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || !validSHA256(checksum) {
		return BuildArtifact{}, fmt.Errorf("build artifact is invalid")
	}
	return BuildArtifact{path: artifactPath, checksum: checksum, sizeBytes: sizeBytes}, nil
}

func (a BuildArtifact) Path() string      { return a.path }
func (a BuildArtifact) Checksum() string  { return a.checksum }
func (a BuildArtifact) SizeBytes() uint64 { return a.sizeBytes }
func (a BuildArtifact) valid() bool {
	clean := path.Clean(a.path)
	return clean == a.path && clean != "." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/") && validSHA256(a.checksum)
}

type InitData struct {
	targets    []BuildTarget
	outputRoot string
	artifacts  []BuildArtifact
}

func NewInitData(targets []BuildTarget, outputRoot string, artifacts []BuildArtifact) (InitData, error) {
	if len(targets) == 0 || len(targets) > 2 || !boundedText(outputRoot, 4096, false) || len(artifacts) == 0 || len(artifacts) > 4096 {
		return InitData{}, fmt.Errorf("init data is incomplete")
	}
	ownedTargets := append([]BuildTarget(nil), targets...)
	sort.Slice(ownedTargets, func(i, j int) bool { return ownedTargets[i] < ownedTargets[j] })
	for index, target := range ownedTargets {
		if !target.Valid() || index > 0 && ownedTargets[index-1] == target {
			return InitData{}, fmt.Errorf("init targets are invalid")
		}
	}
	ownedArtifacts := append([]BuildArtifact(nil), artifacts...)
	sort.Slice(ownedArtifacts, func(i, j int) bool { return ownedArtifacts[i].path < ownedArtifacts[j].path })
	for index, artifact := range ownedArtifacts {
		if !artifact.valid() || index > 0 && ownedArtifacts[index-1].path == artifact.path {
			return InitData{}, fmt.Errorf("init artifacts are invalid")
		}
	}
	return InitData{targets: ownedTargets, outputRoot: outputRoot, artifacts: ownedArtifacts}, nil
}

func (InitData) cliData()                     {}
func (d InitData) Targets() []BuildTarget     { return append([]BuildTarget(nil), d.targets...) }
func (d InitData) OutputRoot() string         { return d.outputRoot }
func (d InitData) ValidationValid() bool      { return true }
func (d InitData) Reproducible() bool         { return true }
func (d InitData) Artifacts() []BuildArtifact { return append([]BuildArtifact(nil), d.artifacts...) }
func (d InitData) valid() bool {
	if len(d.targets) == 0 || len(d.targets) > 2 || !boundedText(d.outputRoot, 4096, false) || len(d.artifacts) == 0 || len(d.artifacts) > 4096 {
		return false
	}
	for index, target := range d.targets {
		if !target.Valid() || index > 0 && d.targets[index-1] >= target {
			return false
		}
	}
	for index, artifact := range d.artifacts {
		if !artifact.valid() || index > 0 && d.artifacts[index-1].path >= artifact.path {
			return false
		}
	}
	return true
}

type BuildSelection struct {
	asset       string
	variant     string
	reason      string
	requestedBy string
}

func NewBuildSelection(asset, variant, reason, requestedBy string) (BuildSelection, error) {
	if !contentIdentifierPattern.MatchString(asset) || !contentIdentifierPattern.MatchString(variant) ||
		(reason != "all" && reason != "explicit" && reason != "bundle" && reason != "dependency" && reason != "native_unit") ||
		!boundedText(requestedBy, 256, false) {
		return BuildSelection{}, fmt.Errorf("build selection is invalid")
	}
	return BuildSelection{asset: asset, variant: variant, reason: reason, requestedBy: requestedBy}, nil
}

func (s BuildSelection) Asset() string       { return s.asset }
func (s BuildSelection) Variant() string     { return s.variant }
func (s BuildSelection) Reason() string      { return s.reason }
func (s BuildSelection) RequestedBy() string { return s.requestedBy }
func (s BuildSelection) valid() bool {
	_, err := NewBuildSelection(s.asset, s.variant, s.reason, s.requestedBy)
	return err == nil
}

type BuildData struct {
	source       Source
	target       BuildTarget
	host         BuildHost
	outputRoot   string
	reproducible bool
	artifacts    []BuildArtifact
	selection    []BuildSelection
	content      []ContentItem
}

func NewBuildData(source Source, target BuildTarget, host BuildHost, outputRoot string, reproducible bool, artifacts []BuildArtifact, content []ContentItem) (BuildData, error) {
	selection := make([]BuildSelection, 0, len(content))
	for _, item := range content {
		entry, err := NewBuildSelection(item.Identifier(), "default", "all", "--all")
		if err != nil {
			return BuildData{}, err
		}
		selection = append(selection, entry)
	}
	return NewBuildDataWithSelection(source, target, host, outputRoot, reproducible, artifacts, selection, content)
}

func NewBuildDataWithSelection(source Source, target BuildTarget, host BuildHost, outputRoot string, reproducible bool, artifacts []BuildArtifact, selection []BuildSelection, content []ContentItem) (BuildData, error) {
	if !source.valid() || !target.Valid() || !host.Valid() || !boundedText(outputRoot, 4096, false) || len(artifacts) == 0 || len(artifacts) > 4096 || !validContent(content) {
		return BuildData{}, fmt.Errorf("build data is incomplete")
	}
	owned := append([]BuildArtifact(nil), artifacts...)
	for _, artifact := range owned {
		if !artifact.valid() {
			return BuildData{}, fmt.Errorf("build data contains an invalid artifact")
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].path < owned[j].path })
	for index := 1; index < len(owned); index++ {
		if owned[index-1].path == owned[index].path {
			return BuildData{}, fmt.Errorf("build data contains duplicate artifacts")
		}
	}
	ownedSelection := append([]BuildSelection(nil), selection...)
	sort.Slice(ownedSelection, func(i, j int) bool { return ownedSelection[i].asset < ownedSelection[j].asset })
	for index, entry := range ownedSelection {
		if !entry.valid() || index > 0 && ownedSelection[index-1].asset >= entry.asset {
			return BuildData{}, fmt.Errorf("build selection is invalid or duplicated")
		}
	}
	return BuildData{source: source, target: target, host: host, outputRoot: outputRoot, reproducible: reproducible, artifacts: owned, selection: ownedSelection, content: sortedContent(content)}, nil
}

func (BuildData) cliData()                       {}
func (d BuildData) Source() Source               { return d.source }
func (d BuildData) Target() BuildTarget          { return d.target }
func (d BuildData) Host() BuildHost              { return d.host }
func (d BuildData) OutputRoot() string           { return d.outputRoot }
func (d BuildData) Reproducible() bool           { return d.reproducible }
func (d BuildData) ValidationValid() bool        { return true }
func (d BuildData) Artifacts() []BuildArtifact   { return append([]BuildArtifact(nil), d.artifacts...) }
func (d BuildData) Selection() []BuildSelection  { return append([]BuildSelection(nil), d.selection...) }
func (d BuildData) ActiveContent() []ContentItem { return append([]ContentItem(nil), d.content...) }
func (d BuildData) valid() bool {
	if !d.source.valid() || !d.target.Valid() || !d.host.Valid() || !boundedText(d.outputRoot, 4096, false) || len(d.artifacts) == 0 || len(d.artifacts) > 4096 {
		return false
	}
	for index, artifact := range d.artifacts {
		if !artifact.valid() || index > 0 && d.artifacts[index-1].path >= artifact.path {
			return false
		}
	}
	for index, entry := range d.selection {
		if !entry.valid() || index > 0 && d.selection[index-1].asset >= entry.asset {
			return false
		}
	}
	for _, item := range d.content {
		if !item.valid() {
			return false
		}
	}
	return true
}

type PlanData struct {
	operation    Operation
	source       Source
	hasSource    bool
	installation domain.InstallationID
	actions      []Action
	content      []ContentItem
	conflicts    []Conflict
	final        FinalState
	disposition  result.UpdateDisposition
}

func NewPlanData(operation Operation, source Source, installation domain.InstallationID, actions []Action, content []ContentItem, conflicts []Conflict, final FinalState, disposition result.UpdateDisposition) (PlanData, error) {
	if !operation.Valid() || !source.valid() || !installation.Valid() || !final.valid() || !validUpdateDisposition(disposition) || !validActions(actions) || !validContent(content) || len(conflicts) > 64 || !validConflicts(conflicts) {
		return PlanData{}, fmt.Errorf("plan data is incomplete")
	}
	return PlanData{operation: operation, source: source, hasSource: true, installation: installation, actions: sortedActions(actions), content: sortedContent(content), conflicts: sortedConflicts(conflicts), final: final, disposition: disposition}, nil
}

func NewOfflinePlanData(operation Operation, installation domain.InstallationID, actions []Action, conflicts []Conflict, final FinalState) (PlanData, error) {
	if operation != OperationHistoryPurge || !installation.Valid() || !final.valid() || !validActions(actions) || len(conflicts) > 64 || !validConflicts(conflicts) {
		return PlanData{}, fmt.Errorf("offline plan data is incomplete")
	}
	return PlanData{operation: operation, installation: installation, actions: sortedActions(actions), conflicts: sortedConflicts(conflicts), final: final, disposition: result.UpdateNotChecked}, nil
}
func (PlanData) cliData()                                      {}
func (d PlanData) Operation() Operation                        { return d.operation }
func (d PlanData) Source() Source                              { return d.source }
func (d PlanData) HasSource() bool                             { return d.hasSource }
func (d PlanData) InstallationID() domain.InstallationID       { return d.installation }
func (d PlanData) Actions() []Action                           { return append([]Action(nil), d.actions...) }
func (d PlanData) ActiveContent() []ContentItem                { return append([]ContentItem(nil), d.content...) }
func (d PlanData) Conflicts() []Conflict                       { return append([]Conflict(nil), d.conflicts...) }
func (d PlanData) ExpectedFinalState() FinalState              { return d.final }
func (d PlanData) UpdateDisposition() result.UpdateDisposition { return d.disposition }
func (d PlanData) valid() bool {
	if !d.operation.Valid() || d.hasSource != d.source.valid() || !d.hasSource && d.operation != OperationHistoryPurge || !d.installation.Valid() || !d.final.valid() || !validUpdateDisposition(d.disposition) {
		return false
	}
	for _, item := range d.actions {
		if !item.valid() {
			return false
		}
	}
	for _, item := range d.content {
		if !item.valid() {
			return false
		}
	}
	for _, item := range d.conflicts {
		if !item.valid() {
			return false
		}
	}
	return true
}

type MutationData struct {
	operation       Operation
	operationResult result.Result
	installation    domain.InstallationID
	actions         []Action
	final           FinalState
	disposition     result.UpdateDisposition
}

func NewMutationData(operation Operation, operationResult result.Result, installation *domain.InstallationID, actions []Action, final FinalState, disposition result.UpdateDisposition) (MutationData, error) {
	if !operation.Valid() || !operationResult.Valid() || !final.valid() || !validUpdateDisposition(disposition) || !validActions(actions) {
		return MutationData{}, fmt.Errorf("operation data is incomplete")
	}
	var id domain.InstallationID
	if installation != nil {
		if !installation.Valid() {
			return MutationData{}, fmt.Errorf("invalid installation ID")
		}
		id = *installation
	}
	return MutationData{operation: operation, operationResult: operationResult, installation: id, actions: sortedActions(actions), final: final, disposition: disposition}, nil
}
func (MutationData) cliData()                                      {}
func (d MutationData) Operation() Operation                        { return d.operation }
func (d MutationData) OperationResult() result.Result              { return d.operationResult }
func (d MutationData) InstallationID() domain.InstallationID       { return d.installation }
func (d MutationData) HasInstallationID() bool                     { return d.installation.Valid() }
func (d MutationData) AppliedActions() []Action                    { return append([]Action(nil), d.actions...) }
func (d MutationData) FinalState() FinalState                      { return d.final }
func (d MutationData) UpdateDisposition() result.UpdateDisposition { return d.disposition }
func (d MutationData) valid() bool {
	if !d.operation.Valid() || !d.operationResult.Valid() || !d.final.valid() || !validUpdateDisposition(d.disposition) {
		return false
	}
	for _, item := range d.actions {
		if !item.valid() {
			return false
		}
	}
	return true
}

type HistoryDescriptor struct {
	operationID domain.OperationID
	operation   Operation
	timestamp   time.Time
	restorable  bool
}

func NewHistoryDescriptor(operationID domain.OperationID, operation Operation, timestamp time.Time, restorable bool) (HistoryDescriptor, error) {
	if !operationID.Valid() || !operation.Valid() || operation == OperationHistoryPurge || timestamp.IsZero() || timestamp.Location() != time.UTC {
		return HistoryDescriptor{}, fmt.Errorf("history descriptor is incomplete")
	}
	return HistoryDescriptor{operationID: operationID, operation: operation, timestamp: timestamp, restorable: restorable}, nil
}

func (d HistoryDescriptor) OperationID() domain.OperationID { return d.operationID }
func (d HistoryDescriptor) Operation() Operation            { return d.operation }
func (d HistoryDescriptor) Timestamp() time.Time            { return d.timestamp }
func (d HistoryDescriptor) Restorable() bool                { return d.restorable }
func (d HistoryDescriptor) valid() bool {
	return d.operationID.Valid() && d.operation.Valid() && d.operation != OperationHistoryPurge && !d.timestamp.IsZero() && d.timestamp.Location() == time.UTC
}

type HistoryData struct {
	installation domain.InstallationID
	entries      []HistoryDescriptor
}

func NewHistoryData(installation domain.InstallationID, entries []HistoryDescriptor) (HistoryData, error) {
	if !installation.Valid() || len(entries) > 1024 {
		return HistoryData{}, fmt.Errorf("history data is incomplete")
	}
	items := append([]HistoryDescriptor(nil), entries...)
	sort.Slice(items, func(left, right int) bool {
		if items[left].timestamp.Equal(items[right].timestamp) {
			return items[left].operationID.String() < items[right].operationID.String()
		}
		return items[left].timestamp.Before(items[right].timestamp)
	})
	data := HistoryData{installation: installation, entries: items}
	if !data.valid() {
		return HistoryData{}, fmt.Errorf("history data is incomplete")
	}
	return data, nil
}

func (HistoryData) cliData()                                {}
func (d HistoryData) InstallationID() domain.InstallationID { return d.installation }
func (d HistoryData) Entries() []HistoryDescriptor {
	return append([]HistoryDescriptor(nil), d.entries...)
}
func (d HistoryData) valid() bool {
	if !d.installation.Valid() || len(d.entries) > 1024 {
		return false
	}
	for index, entry := range d.entries {
		if !entry.valid() || index > 0 && (d.entries[index-1].timestamp.After(entry.timestamp) || d.entries[index-1].timestamp.Equal(entry.timestamp) && d.entries[index-1].operationID.String() >= entry.operationID.String()) {
			return false
		}
	}
	return true
}

// RecordedSource is the source intent durably retained for an installation.
// It deliberately omits validation-only tree and renderer provenance that the
// Installation status omits ownership details that are unavailable locally.
type RecordedSource struct {
	mode         SourceMode
	selection    domain.SourceSelection
	repository   domain.RepositoryIdentity
	requestedRef string
	hasRequested bool
	refKind      RefKind
	commit       domain.CommitOID
	checkout     string
	sourceDigest domain.RenderedDigest
	dirty        bool
}

func NewRecordedSource(selection domain.SourceSelection, repository domain.RepositoryIdentity, requestedRef string, hasRequested bool, refKind RefKind, commit domain.CommitOID) (RecordedSource, error) {
	if (selection != domain.BuiltInDefaultSource() && selection != domain.ExplicitSource()) || !repository.Valid() || !refKind.Valid() || !commit.Valid() ||
		(refKind == RefDefaultBranch) != !hasRequested || !hasRequested && requestedRef != "" {
		return RecordedSource{}, fmt.Errorf("recorded source is incomplete")
	}
	if selection == domain.BuiltInDefaultSource() && repository.String() != "github.com/alx4j/ai4j" {
		return RecordedSource{}, fmt.Errorf("recorded source is incomplete")
	}
	if hasRequested {
		if _, err := gitsource.NewRequestedReference(requestedRef); err != nil {
			return RecordedSource{}, fmt.Errorf("recorded source is incomplete")
		}
	}
	if refKind == RefCommit && requestedRef != commit.String() {
		return RecordedSource{}, fmt.Errorf("recorded source is incomplete")
	}
	return RecordedSource{mode: SourceGitHub, selection: selection, repository: repository, requestedRef: requestedRef, hasRequested: hasRequested, refKind: refKind, commit: commit}, nil
}

func NewRecordedDevelopmentSource(checkout string, sourceDigest domain.RenderedDigest, dirty bool) (RecordedSource, error) {
	if !filepath.IsAbs(checkout) || !sourceDigest.Valid() {
		return RecordedSource{}, fmt.Errorf("recorded development source is incomplete")
	}
	return RecordedSource{mode: SourceDevelopment, selection: domain.ExplicitSource(), checkout: filepath.Clean(checkout), sourceDigest: sourceDigest, dirty: dirty}, nil
}

func (s RecordedSource) Mode() SourceMode                    { return s.mode }
func (s RecordedSource) Checkout() string                    { return s.checkout }
func (s RecordedSource) SourceDigest() domain.RenderedDigest { return s.sourceDigest }
func (s RecordedSource) Dirty() bool                         { return s.dirty }

func (s RecordedSource) Selection() domain.SourceSelection     { return s.selection }
func (s RecordedSource) Repository() domain.RepositoryIdentity { return s.repository }
func (s RecordedSource) RequestedRef() string                  { return s.requestedRef }
func (s RecordedSource) HasRequestedRef() bool                 { return s.hasRequested }
func (s RecordedSource) RefKind() RefKind                      { return s.refKind }
func (s RecordedSource) Commit() domain.CommitOID              { return s.commit }
func (s RecordedSource) valid() bool {
	if s.mode == SourceDevelopment {
		_, err := NewRecordedDevelopmentSource(s.checkout, s.sourceDigest, s.dirty)
		return err == nil
	}
	_, err := NewRecordedSource(s.selection, s.repository, s.requestedRef, s.hasRequested, s.refKind, s.commit)
	return err == nil
}

type Installation struct {
	id                         domain.InstallationID
	toolkitID, pluginID        string
	source                     RecordedSource
	toolkitVersion, cliVersion string
	expectedNativeVersion      string
}

func NewInstallation(id domain.InstallationID, toolkitID, pluginID string, source RecordedSource, toolkitVersion, cliVersion, expectedNativeVersion string) (Installation, error) {
	if !id.Valid() || !hyphenatedIdentifierPattern.MatchString(toolkitID) || !pluginIdentifierPattern.MatchString(pluginID) || !source.valid() || !boundedText(toolkitVersion, 128, false) || !boundedText(cliVersion, 128, false) || (expectedNativeVersion != "" && !boundedText(expectedNativeVersion, 128, false)) {
		return Installation{}, fmt.Errorf("installation is incomplete")
	}
	return Installation{id: id, toolkitID: toolkitID, pluginID: pluginID, source: source, toolkitVersion: toolkitVersion, cliVersion: cliVersion, expectedNativeVersion: expectedNativeVersion}, nil
}
func (i Installation) ID() domain.InstallationID      { return i.id }
func (i Installation) ToolkitID() string              { return i.toolkitID }
func (i Installation) NativePluginID() string         { return i.pluginID }
func (i Installation) Source() RecordedSource         { return i.source }
func (i Installation) ToolkitVersion() string         { return i.toolkitVersion }
func (i Installation) CLIVersion() string             { return i.cliVersion }
func (i Installation) ExpectedNativeVersion() string  { return i.expectedNativeVersion }
func (i Installation) HasExpectedNativeVersion() bool { return i.expectedNativeVersion != "" }

type NativeRegistration string

const (
	NativeRegistered                NativeRegistration = "registered"
	NativeNotRegistered             NativeRegistration = "not_registered"
	NativeRegistrationUnknown       NativeRegistration = "unknown"
	NativeRegistrationNotObservable NativeRegistration = "not_observable"
)

type NativeInstallation string

const (
	NativeInstalled                 NativeInstallation = "installed"
	NativeNotInstalled              NativeInstallation = "not_installed"
	NativeInstallationUnknown       NativeInstallation = "unknown"
	NativeInstallationNotObservable NativeInstallation = "not_observable"
)

type NativeEnablement string

const (
	NativeEnabled                 NativeEnablement = "enabled"
	NativeDisabled                NativeEnablement = "disabled"
	NativeEnablementUnknown       NativeEnablement = "unknown"
	NativeEnablementNotObservable NativeEnablement = "not_observable"
)

type NativeActivation string

const (
	NativeActive                  NativeActivation = "active"
	NativeInactive                NativeActivation = "inactive"
	NativeActivationUnknown       NativeActivation = "unknown"
	NativeActivationNotObservable NativeActivation = "not_observable"
)

type NativeReload string

const (
	NativeReloadRequired      NativeReload = "required"
	NativeReloadNotRequired   NativeReload = "not_required"
	NativeReloadUnknown       NativeReload = "unknown"
	NativeReloadNotObservable NativeReload = "not_observable"
)

type NativeNextSession string

const (
	NativeNextSessionRequired      NativeNextSession = "required"
	NativeNextSessionNotRequired   NativeNextSession = "not_required"
	NativeNextSessionUnknown       NativeNextSession = "unknown"
	NativeNextSessionNotObservable NativeNextSession = "not_observable"
)

type NativePolicy string

const (
	NativePolicyAllowed       NativePolicy = "allowed"
	NativePolicyBlocked       NativePolicy = "policy_blocked"
	NativePolicyUnknown       NativePolicy = "unknown"
	NativePolicyNotObservable NativePolicy = "not_observable"
)

type NativeVersionStatus string

const (
	NativeVersionMatches       NativeVersionStatus = "matches"
	NativeVersionMismatch      NativeVersionStatus = "mismatch"
	NativeVersionNotApplicable NativeVersionStatus = "not_applicable"
	NativeVersionUnknown       NativeVersionStatus = "unknown"
	NativeVersionNotObservable NativeVersionStatus = "not_observable"
)

type NativeState struct {
	registration  NativeRegistration
	installation  NativeInstallation
	enablement    NativeEnablement
	activation    NativeActivation
	reload        NativeReload
	nextSession   NativeNextSession
	policy        NativePolicy
	version       string
	versionStatus NativeVersionStatus
}

func NewNativeState(registration NativeRegistration, installation NativeInstallation, enablement NativeEnablement, activation NativeActivation, reload NativeReload, nextSession NativeNextSession, policy NativePolicy, version string, versionStatus NativeVersionStatus) (NativeState, error) {
	if !validNativeRegistration(registration) || !validNativeInstallation(installation) || !validNativeEnablement(enablement) || !validNativeActivation(activation) || !validNativeReload(reload) || !validNativeNextSession(nextSession) || !validNativePolicy(policy) || (version != "" && !boundedText(version, 128, false)) || !validNativeVersionStatus(versionStatus) {
		return NativeState{}, fmt.Errorf("native state contains an unknown value")
	}
	return NativeState{registration: registration, installation: installation, enablement: enablement, activation: activation, reload: reload, nextSession: nextSession, policy: policy, version: version, versionStatus: versionStatus}, nil
}
func (s NativeState) Registration() NativeRegistration   { return s.registration }
func (s NativeState) Installation() NativeInstallation   { return s.installation }
func (s NativeState) Enablement() NativeEnablement       { return s.enablement }
func (s NativeState) Activation() NativeActivation       { return s.activation }
func (s NativeState) Reload() NativeReload               { return s.reload }
func (s NativeState) NextSession() NativeNextSession     { return s.nextSession }
func (s NativeState) Policy() NativePolicy               { return s.policy }
func (s NativeState) Version() string                    { return s.version }
func (s NativeState) HasVersion() bool                   { return s.version != "" }
func (s NativeState) VersionStatus() NativeVersionStatus { return s.versionStatus }

func validNativeRegistration(v NativeRegistration) bool {
	return v == NativeRegistered || v == NativeNotRegistered || v == NativeRegistrationUnknown || v == NativeRegistrationNotObservable
}
func validNativeInstallation(v NativeInstallation) bool {
	return v == NativeInstalled || v == NativeNotInstalled || v == NativeInstallationUnknown || v == NativeInstallationNotObservable
}
func validNativeEnablement(v NativeEnablement) bool {
	return v == NativeEnabled || v == NativeDisabled || v == NativeEnablementUnknown || v == NativeEnablementNotObservable
}
func validNativeActivation(v NativeActivation) bool {
	return v == NativeActive || v == NativeInactive || v == NativeActivationUnknown || v == NativeActivationNotObservable
}
func validNativeReload(v NativeReload) bool {
	return v == NativeReloadRequired || v == NativeReloadNotRequired || v == NativeReloadUnknown || v == NativeReloadNotObservable
}
func validNativeNextSession(v NativeNextSession) bool {
	return v == NativeNextSessionRequired || v == NativeNextSessionNotRequired || v == NativeNextSessionUnknown || v == NativeNextSessionNotObservable
}
func validNativePolicy(v NativePolicy) bool {
	return v == NativePolicyAllowed || v == NativePolicyBlocked || v == NativePolicyUnknown || v == NativePolicyNotObservable
}

func validNativeVersionStatus(v NativeVersionStatus) bool {
	return v == NativeVersionMatches || v == NativeVersionMismatch || v == NativeVersionNotApplicable || v == NativeVersionUnknown || v == NativeVersionNotObservable
}

type DriftState string

const (
	DriftUnchanged   DriftState = "unchanged"
	DriftModified    DriftState = "modified"
	DriftMissing     DriftState = "missing"
	DriftConflicting DriftState = "conflicting"
)

type Drift struct {
	resource string
	state    DriftState
}

func NewDrift(resource string, state DriftState) (Drift, error) {
	if !boundedText(resource, 256, false) || (state != DriftUnchanged && state != DriftModified && state != DriftMissing && state != DriftConflicting) {
		return Drift{}, fmt.Errorf("drift is incomplete")
	}
	return Drift{resource: resource, state: state}, nil
}
func (d Drift) Resource() string  { return d.resource }
func (d Drift) State() DriftState { return d.state }

type RecoveryKind string

const (
	RecoveryStateNone         RecoveryKind = "none"
	RecoveryIncompleteJournal RecoveryKind = "incomplete_journal"
	RecoveryCleanupRequired   RecoveryKind = "cleanup_required"
	RecoveryUnsupportedSchema RecoveryKind = "unsupported_schema"
	RecoveryUnknown           RecoveryKind = "unknown"
)

type RecoveryState struct {
	state RecoveryKind
	phase result.Phase
}

func NewRecoveryState(state RecoveryKind, phase result.Phase) (RecoveryState, error) {
	stateValid := state == RecoveryStateNone || state == RecoveryIncompleteJournal || state == RecoveryCleanupRequired || state == RecoveryUnsupportedSchema || state == RecoveryUnknown
	phaseValid := false
	switch state {
	case RecoveryIncompleteJournal:
		phaseValid = phase == "" || phase == result.PhasePrepared || phase == result.PhaseApplying || phase == result.PhaseReconciled || phase == result.PhaseCompensating
	case RecoveryCleanupRequired:
		phaseValid = phase == result.PhaseCommittedCleanupPending || phase == result.PhaseRolledBackCleanupPending
	default:
		phaseValid = phase == ""
	}
	if !stateValid || !phaseValid {
		return RecoveryState{}, fmt.Errorf("recovery state is incomplete")
	}
	return RecoveryState{state: state, phase: phase}, nil
}
func (r RecoveryState) State() RecoveryKind { return r.state }
func (r RecoveryState) Phase() result.Phase { return r.phase }
func (r RecoveryState) HasPhase() bool      { return r.phase != "" }

type InstallationSummary struct {
	id            domain.InstallationID
	toolkitID     string
	target        BuildTarget
	scope         Scope
	scopeRoot     string
	lifecycle     string
	source        RecordedSource
	all           bool
	assets        []string
	bundles       []string
	resolved      []string
	health        string
	historyCount  int
	lastOperation domain.OperationID
}

func NewInstallationSummary(id domain.InstallationID, toolkitID string, target BuildTarget, scope Scope, scopeRoot, lifecycle string, source RecordedSource, all bool, assets, bundles, resolved []string, health string) (InstallationSummary, error) {
	return NewDetailedInstallationSummary(id, toolkitID, target, scope, scopeRoot, lifecycle, source, all, assets, bundles, resolved, health, 0, domain.OperationID{})
}

func NewDetailedInstallationSummary(id domain.InstallationID, toolkitID string, target BuildTarget, scope Scope, scopeRoot, lifecycle string, source RecordedSource, all bool, assets, bundles, resolved []string, health string, historyCount int, lastOperation domain.OperationID) (InstallationSummary, error) {
	value := InstallationSummary{id: id, toolkitID: toolkitID, target: target, scope: scope, scopeRoot: scopeRoot, lifecycle: lifecycle, source: source, all: all, assets: uniqueSortedStrings(assets), bundles: uniqueSortedStrings(bundles), resolved: uniqueSortedStrings(resolved), health: health, historyCount: historyCount, lastOperation: lastOperation}
	if !value.valid() {
		return InstallationSummary{}, fmt.Errorf("installation summary is incomplete")
	}
	return value, nil
}

func (i InstallationSummary) ID() domain.InstallationID           { return i.id }
func (i InstallationSummary) ToolkitID() string                   { return i.toolkitID }
func (i InstallationSummary) Target() BuildTarget                 { return i.target }
func (i InstallationSummary) Scope() Scope                        { return i.scope }
func (i InstallationSummary) ScopeRoot() string                   { return i.scopeRoot }
func (i InstallationSummary) Lifecycle() string                   { return i.lifecycle }
func (i InstallationSummary) Source() RecordedSource              { return i.source }
func (i InstallationSummary) SelectAll() bool                     { return i.all }
func (i InstallationSummary) Assets() []string                    { return cloneStrings(i.assets) }
func (i InstallationSummary) Bundles() []string                   { return cloneStrings(i.bundles) }
func (i InstallationSummary) Resolved() []string                  { return cloneStrings(i.resolved) }
func (i InstallationSummary) Health() string                      { return i.health }
func (i InstallationSummary) HistoryCount() int                   { return i.historyCount }
func (i InstallationSummary) LastOperationID() domain.OperationID { return i.lastOperation }
func (i InstallationSummary) HasLastOperationID() bool            { return i.lastOperation.Valid() }
func (i InstallationSummary) valid() bool {
	if !i.id.Valid() || !hyphenatedIdentifierPattern.MatchString(i.toolkitID) || !i.target.Valid() || !i.scope.Valid() ||
		!boundedText(i.scopeRoot, 4096, false) || (i.lifecycle != "active" && i.lifecycle != "archived") || !i.source.valid() ||
		(i.all && (len(i.assets) != 0 || len(i.bundles) != 0)) || (!i.all && len(i.assets) == 0 && len(i.bundles) == 0) ||
		(i.health != "healthy" && i.health != "drifted" && i.health != "unknown" && i.health != "recovery_required") || i.historyCount < 0 || i.historyCount > 1024 || i.historyCount > 0 && !i.lastOperation.Valid() {
		return false
	}
	return hasUniqueSortedStrings(i.assets) && hasUniqueSortedStrings(i.bundles) && hasUniqueSortedStrings(i.resolved)
}

type ListData struct {
	installations []InstallationSummary
}

func NewListData(installations []InstallationSummary) (ListData, error) {
	items := append([]InstallationSummary(nil), installations...)
	for _, item := range items {
		if !item.valid() {
			return ListData{}, fmt.Errorf("list data contains an invalid installation")
		}
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].lifecycle != items[right].lifecycle {
			return items[left].lifecycle < items[right].lifecycle
		}
		return items[left].id.String() < items[right].id.String()
	})
	return ListData{installations: items}, nil
}

func (ListData) cliData() {}
func (d ListData) Installations() []InstallationSummary {
	return append([]InstallationSummary(nil), d.installations...)
}
func (d ListData) valid() bool {
	for index, item := range d.installations {
		if !item.valid() || index > 0 && (d.installations[index-1].lifecycle > item.lifecycle || d.installations[index-1].lifecycle == item.lifecycle && d.installations[index-1].id.String() >= item.id.String()) {
			return false
		}
	}
	return true
}

type StatusData struct {
	installation *Installation
	summary      *InstallationSummary
	native       NativeState
	drift        []Drift
	recovery     RecoveryState
	disposition  result.UpdateDisposition
}

type DoctorCheckStatus string

const (
	DoctorCheckOK      DoctorCheckStatus = "ok"
	DoctorCheckWarning DoctorCheckStatus = "warning"
	DoctorCheckError   DoctorCheckStatus = "error"
)

type DoctorCheck struct {
	id      string
	status  DoctorCheckStatus
	summary string
}

func NewDoctorCheck(id string, status DoctorCheckStatus, summary string) (DoctorCheck, error) {
	value := DoctorCheck{id: id, status: status, summary: summary}
	if !value.valid() {
		return DoctorCheck{}, fmt.Errorf("doctor check is incomplete")
	}
	return value, nil
}

func (c DoctorCheck) ID() string                { return c.id }
func (c DoctorCheck) Status() DoctorCheckStatus { return c.status }
func (c DoctorCheck) Summary() string           { return c.summary }
func (c DoctorCheck) valid() bool {
	return doctorCheckIDPattern.MatchString(c.id) &&
		(c.status == DoctorCheckOK || c.status == DoctorCheckWarning || c.status == DoctorCheckError) &&
		boundedText(c.summary, 512, false)
}

type MCPStartupCheck struct {
	serverID    string
	executable  string
	arguments   []string
	environment []string
	workingDir  string
	ownership   string
	result      string
	exitCode    int
	hasExitCode bool
}

func NewMCPStartupCheck(serverID, executable string, arguments, environment []string, workingDir, ownership, startupResult string, exitCode int, hasExitCode bool) (MCPStartupCheck, error) {
	value := MCPStartupCheck{
		serverID: serverID, executable: executable, arguments: cloneStrings(arguments), environment: uniqueSortedStrings(environment),
		workingDir: workingDir, ownership: ownership, result: startupResult, exitCode: exitCode, hasExitCode: hasExitCode,
	}
	if !value.valid() {
		return MCPStartupCheck{}, fmt.Errorf("MCP startup check is incomplete")
	}
	return value, nil
}

func (c MCPStartupCheck) ServerID() string         { return c.serverID }
func (c MCPStartupCheck) Executable() string       { return c.executable }
func (c MCPStartupCheck) Arguments() []string      { return cloneStrings(c.arguments) }
func (c MCPStartupCheck) Environment() []string    { return cloneStrings(c.environment) }
func (c MCPStartupCheck) WorkingDirectory() string { return c.workingDir }
func (c MCPStartupCheck) Ownership() string        { return c.ownership }
func (c MCPStartupCheck) Result() string           { return c.result }
func (c MCPStartupCheck) ExitCode() int            { return c.exitCode }
func (c MCPStartupCheck) HasExitCode() bool        { return c.hasExitCode }
func (c MCPStartupCheck) valid() bool {
	if !hyphenatedIdentifierPattern.MatchString(c.serverID) || !boundedText(c.executable, 4096, false) ||
		!boundedText(c.workingDir, 4096, false) || c.ownership != "package" ||
		(c.result != "not_run" && c.result != "started" && c.result != "exited" && c.result != "timed_out" && c.result != "failed") ||
		len(c.arguments) > 256 || len(c.environment) > 128 {
		return false
	}
	for _, argument := range c.arguments {
		if !boundedText(argument, 16<<10, true) {
			return false
		}
	}
	for index, name := range c.environment {
		if !environmentNamePattern.MatchString(name) || index > 0 && c.environment[index-1] >= name {
			return false
		}
	}
	return c.hasExitCode || c.exitCode == 0
}

type DoctorData struct {
	installation domain.InstallationID
	checks       []DoctorCheck
	startup      *MCPStartupCheck
}

func NewDoctorData(installation domain.InstallationID, checks []DoctorCheck, startup *MCPStartupCheck) (DoctorData, error) {
	value := DoctorData{installation: installation, checks: append([]DoctorCheck(nil), checks...)}
	if startup != nil {
		copy := *startup
		copy.arguments = cloneStrings(startup.arguments)
		copy.environment = cloneStrings(startup.environment)
		value.startup = &copy
	}
	if !value.valid() {
		return DoctorData{}, fmt.Errorf("doctor data is incomplete")
	}
	return value, nil
}

func (DoctorData) cliData()                                {}
func (d DoctorData) InstallationID() domain.InstallationID { return d.installation }
func (d DoctorData) Checks() []DoctorCheck                 { return append([]DoctorCheck(nil), d.checks...) }
func (d DoctorData) StartupCheck() (MCPStartupCheck, bool) {
	if d.startup == nil {
		return MCPStartupCheck{}, false
	}
	return *d.startup, true
}
func (d DoctorData) valid() bool {
	if !d.installation.Valid() || len(d.checks) == 0 || len(d.checks) > 64 || d.startup != nil && !d.startup.valid() {
		return false
	}
	seen := make(map[string]struct{}, len(d.checks))
	for _, check := range d.checks {
		if !check.valid() {
			return false
		}
		if _, duplicate := seen[check.id]; duplicate {
			return false
		}
		seen[check.id] = struct{}{}
	}
	return true
}

func NewStatusData(installation *Installation, native NativeState, drift []Drift, recovery RecoveryState, disposition result.UpdateDisposition) (StatusData, error) {
	return NewDetailedStatusData(installation, nil, native, drift, recovery, disposition)
}

func NewDetailedStatusData(installation *Installation, summary *InstallationSummary, native NativeState, drift []Drift, recovery RecoveryState, disposition result.UpdateDisposition) (StatusData, error) {
	if !native.valid() || !recovery.valid() || !validUpdateDisposition(disposition) || !validDrift(drift) || !nativeVersionSemantics(installation, native) || !statusInstallationDispositionSemantics(installation, recovery, disposition) {
		return StatusData{}, fmt.Errorf("status data is incomplete")
	}
	var owned *Installation
	if installation != nil {
		if !installation.valid() {
			return StatusData{}, fmt.Errorf("status installation is invalid")
		}
		copy := *installation
		owned = &copy
	}
	var ownedSummary *InstallationSummary
	if summary != nil {
		if !summary.valid() || owned == nil || summary.id != owned.id || summary.toolkitID != owned.toolkitID {
			return StatusData{}, fmt.Errorf("status summary is inconsistent")
		}
		copy := *summary
		ownedSummary = &copy
	}
	items := append([]Drift(nil), drift...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].resource == items[j].resource {
			return items[i].state < items[j].state
		}
		return items[i].resource < items[j].resource
	})
	return StatusData{installation: owned, summary: ownedSummary, native: native, drift: items, recovery: recovery, disposition: disposition}, nil
}
func (d StatusData) Summary() (InstallationSummary, bool) {
	if d.summary == nil {
		return InstallationSummary{}, false
	}
	return *d.summary, true
}
func (StatusData) cliData() {}
func (d StatusData) Installation() (Installation, bool) {
	if d.installation == nil {
		return Installation{}, false
	}
	return *d.installation, true
}
func (d StatusData) NativeState() NativeState                    { return d.native }
func (d StatusData) Drift() []Drift                              { return append([]Drift(nil), d.drift...) }
func (d StatusData) RecoveryState() RecoveryState                { return d.recovery }
func (d StatusData) UpdateDisposition() result.UpdateDisposition { return d.disposition }
func (d StatusData) valid() bool {
	if !d.native.valid() || !d.recovery.valid() || !validUpdateDisposition(d.disposition) || !nativeVersionSemantics(d.installation, d.native) || !statusInstallationDispositionSemantics(d.installation, d.recovery, d.disposition) {
		return false
	}
	if d.installation != nil && !d.installation.valid() {
		return false
	}
	if d.summary != nil && (!d.summary.valid() || d.installation == nil || d.summary.id != d.installation.id || d.summary.toolkitID != d.installation.toolkitID) {
		return false
	}
	for _, item := range d.drift {
		if !item.valid() {
			return false
		}
	}
	return true
}

type DefaultRefPolicy string

const (
	DefaultRepositoryBranch DefaultRefPolicy = "repository_default_branch"
	DefaultFixedRef         DefaultRefPolicy = "fixed_ref"
)

type DefaultSource struct {
	repository domain.RepositoryIdentity
	reference  string
	policy     DefaultRefPolicy
}

func NewDefaultSource(repository domain.RepositoryIdentity, reference string, policy DefaultRefPolicy) (DefaultSource, error) {
	if !repository.Valid() || (policy != DefaultRepositoryBranch && policy != DefaultFixedRef) || (policy == DefaultRepositoryBranch && reference != "") || (policy == DefaultFixedRef && !boundedText(reference, 1024, false)) {
		return DefaultSource{}, fmt.Errorf("default source is incomplete")
	}
	return DefaultSource{repository: repository, reference: reference, policy: policy}, nil
}
func (s DefaultSource) Repository() domain.RepositoryIdentity { return s.repository }
func (s DefaultSource) Reference() string                     { return s.reference }
func (s DefaultSource) HasReference() bool                    { return s.reference != "" }
func (s DefaultSource) RefPolicy() DefaultRefPolicy           { return s.policy }

type VersionData struct {
	product, executable, version string
	repository                   domain.RepositoryIdentity
	commit                       domain.BuildCommit
	goVersion                    string
	buildTime                    time.Time
	targetOS, targetArch         string
	defaultSource                DefaultSource
}

func NewVersionData(product, executable, version string, repository domain.RepositoryIdentity, commit domain.BuildCommit, goVersion string, buildTime time.Time, targetOS, targetArch string, defaultSource DefaultSource) (VersionData, error) {
	expectedExecutable := "ai4j"
	if targetOS == "windows" {
		expectedExecutable = "ai4j.exe"
	}
	if product != "AI4J" || executable != expectedExecutable || !boundedText(version, 128, false) || !repository.Valid() || !commit.Valid() || !boundedText(goVersion, 64, false) || !goVersionPattern.MatchString(goVersion) || buildTime.IsZero() || buildTime.Year() < 0 || buildTime.Year() > 9999 || !goTargetPattern.MatchString(targetOS) || !goTargetPattern.MatchString(targetArch) || !defaultSource.valid() {
		return VersionData{}, fmt.Errorf("version data is incomplete")
	}
	return VersionData{product: product, executable: executable, version: version, repository: repository, commit: commit, goVersion: goVersion, buildTime: buildTime.UTC(), targetOS: targetOS, targetArch: targetArch, defaultSource: defaultSource}, nil
}
func (VersionData) cliData()                                         {}
func (d VersionData) Product() string                                { return d.product }
func (d VersionData) Executable() string                             { return d.executable }
func (d VersionData) CLIVersion() string                             { return d.version }
func (d VersionData) CLISourceRepository() domain.RepositoryIdentity { return d.repository }
func (d VersionData) CLISourceCommit() domain.BuildCommit            { return d.commit }
func (d VersionData) GoVersion() string                              { return d.goVersion }
func (d VersionData) BuildTime() time.Time                           { return d.buildTime }
func (d VersionData) TargetOS() string                               { return d.targetOS }
func (d VersionData) TargetArch() string                             { return d.targetArch }
func (d VersionData) DefaultSource() DefaultSource                   { return d.defaultSource }
func (d VersionData) valid() bool {
	return d.product != "" && d.executable != "" && d.version != "" && d.repository.Valid() && d.commit.Valid() && d.goVersion != "" && !d.buildTime.IsZero() && d.targetOS != "" && d.targetArch != "" && d.defaultSource.valid()
}

func sortedContent(values []ContentItem) []ContentItem {
	result := append([]ContentItem(nil), values...)
	sort.Slice(result, func(i, j int) bool { return contentLess(result[i], result[j]) })
	return result
}
func sortedActions(values []Action) []Action {
	result := append([]Action(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].sequence != result[j].sequence {
			return result[i].sequence < result[j].sequence
		}
		if result[i].owner != result[j].owner {
			return result[i].owner < result[j].owner
		}
		if result[i].kind != result[j].kind {
			return result[i].kind < result[j].kind
		}
		if result[i].resource != result[j].resource {
			return result[i].resource < result[j].resource
		}
		left := []string{string(result[i].expectedPrecondition.state), result[i].expectedPrecondition.checksum, string(result[i].proposedPostcondition.state), result[i].proposedPostcondition.checksum, string(result[i].recoveryRequirement)}
		right := []string{string(result[j].expectedPrecondition.state), result[j].expectedPrecondition.checksum, string(result[j].proposedPostcondition.state), result[j].proposedPostcondition.checksum, string(result[j].recoveryRequirement)}
		for index := range left {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return false
	})
	return result
}
func sortedConflicts(values []Conflict) []Conflict {
	result := append([]Conflict(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].code != result[j].code {
			return result[i].code < result[j].code
		}
		if result[i].resource != result[j].resource {
			return result[i].resource < result[j].resource
		}
		return result[i].message < result[j].message
	})
	return result
}

func (v ContentItem) valid() bool {
	_, err := NewContentItem(v.componentType, v.identifier, v.sourcePath, v.checksum, v.change, v.execution)
	return err == nil
}
func (a Action) valid() bool {
	return a.sequence > 0 && validActionOwner(a.owner) && validActionKind(a.kind) && boundedText(a.resource, 256, false) && a.expectedPrecondition.valid() && a.proposedPostcondition.valid() && validRecoveryRequirement(a.recoveryRequirement)
}
func (c Conflict) valid() bool {
	_, err := NewConflict(c.code, c.resource, c.message)
	return err == nil
}
func (s FinalState) valid() bool {
	return s.installation.valid() && s.native.valid() && s.owned.valid()
}
func (i Installation) valid() bool {
	_, err := NewInstallation(i.id, i.toolkitID, i.pluginID, i.source, i.toolkitVersion, i.cliVersion, i.expectedNativeVersion)
	return err == nil
}
func (s NativeState) valid() bool {
	_, err := NewNativeState(s.registration, s.installation, s.enablement, s.activation, s.reload, s.nextSession, s.policy, s.version, s.versionStatus)
	return err == nil
}

func nativeVersionSemantics(installation *Installation, native NativeState) bool {
	if installation == nil || !installation.HasExpectedNativeVersion() {
		return native.versionStatus == NativeVersionNotApplicable && !native.HasVersion()
	}
	expected := installation.ExpectedNativeVersion()
	switch native.versionStatus {
	case NativeVersionMatches:
		return native.HasVersion() && native.Version() == expected
	case NativeVersionMismatch:
		return native.HasVersion() && native.Version() != expected
	case NativeVersionUnknown, NativeVersionNotObservable:
		return !native.HasVersion()
	default:
		return false
	}
}

func statusInstallationDispositionSemantics(installation *Installation, recovery RecoveryState, disposition result.UpdateDisposition) bool {
	if installation == nil && recovery.state == RecoveryStateNone {
		return disposition == result.UpdateNotInstalled
	}
	return installation == nil || disposition != result.UpdateNotInstalled
}

func (d Drift) valid() bool         { _, err := NewDrift(d.resource, d.state); return err == nil }
func (r RecoveryState) valid() bool { _, err := NewRecoveryState(r.state, r.phase); return err == nil }
func (s DefaultSource) valid() bool {
	_, err := NewDefaultSource(s.repository, s.reference, s.policy)
	return err == nil
}

func validUpdateDisposition(value result.UpdateDisposition) bool {
	switch value {
	case result.UpdateNotChecked, result.UpdateNotInstalled, result.UpdateUpToDate, result.UpdateAvailable, result.UpdatePinned, result.UpdateRefRewritten, result.UpdateUnknown:
		return true
	default:
		return false
	}
}

func validPhase(value result.Phase) bool {
	switch value {
	case result.PhaseNone, result.PhasePrepared, result.PhaseApplying, result.PhaseReconciled, result.PhaseCompensating, result.PhaseCommittedCleanupPending, result.PhaseRolledBackCleanupPending, result.PhaseComplete, result.PhaseCompleteRolledBack:
		return true
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func uniqueSortedStrings(values []string) []string {
	output := append([]string(nil), values...)
	sort.Strings(output)
	deduped := output[:0]
	for _, value := range output {
		if len(deduped) == 0 || deduped[len(deduped)-1] != value {
			deduped = append(deduped, value)
		}
	}
	return deduped
}

func hasUniqueSortedStrings(values []string) bool {
	for index, value := range values {
		if !contentIdentifierPattern.MatchString(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func uniqueSortedPlaceholders(values []Placeholder) []Placeholder {
	output := append([]Placeholder(nil), values...)
	sort.Slice(output, func(i, j int) bool { return output[i] < output[j] })
	deduped := output[:0]
	for _, value := range output {
		if len(deduped) == 0 || deduped[len(deduped)-1] != value {
			deduped = append(deduped, value)
		}
	}
	return deduped
}

func validSHA256(value string) bool {
	return sha256Pattern.MatchString(value) && value != strings.Repeat("0", 64)
}
func boundedText(value string, max int, allowEmpty bool) bool {
	if (!allowEmpty && value == "") || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func hasDuplicatePlaceholders(values []Placeholder) bool {
	seen := make(map[Placeholder]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func cloneStrings(values []string) []string {
	output := make([]string, len(values))
	copy(output, values)
	return output
}

func clonePlaceholders(values []Placeholder) []Placeholder {
	output := make([]Placeholder, len(values))
	copy(output, values)
	return output
}

func validContent(values []ContentItem) bool {
	for _, value := range values {
		if !value.valid() {
			return false
		}
	}
	return true
}
func validActions(values []Action) bool {
	for _, value := range values {
		if !value.valid() {
			return false
		}
	}
	return true
}
func validConflicts(values []Conflict) bool {
	for _, value := range values {
		if !value.valid() {
			return false
		}
	}
	return true
}
func validDrift(values []Drift) bool {
	for _, value := range values {
		if !value.valid() {
			return false
		}
	}
	return true
}
func contentLess(left, right ContentItem) bool {
	leftValues := []string{string(left.componentType), left.identifier, left.sourcePath, left.checksum, string(left.change)}
	rightValues := []string{string(right.componentType), right.identifier, right.sourcePath, right.checksum, string(right.change)}
	for index := range leftValues {
		if leftValues[index] != rightValues[index] {
			return leftValues[index] < rightValues[index]
		}
	}
	if (left.execution == nil) != (right.execution == nil) {
		return left.execution == nil
	}
	if left.execution == nil {
		return false
	}
	leftValues = []string{string(left.execution.ownership), string(left.execution.dependency), left.execution.command, left.execution.cwd, strings.Join(left.execution.args, "\x00"), placeholdersKey(left.execution.placeholders), strings.Join(left.execution.environment, "\x00")}
	rightValues = []string{string(right.execution.ownership), string(right.execution.dependency), right.execution.command, right.execution.cwd, strings.Join(right.execution.args, "\x00"), placeholdersKey(right.execution.placeholders), strings.Join(right.execution.environment, "\x00")}
	for index := range leftValues {
		if leftValues[index] != rightValues[index] {
			return leftValues[index] < rightValues[index]
		}
	}
	return false
}

func placeholdersKey(values []Placeholder) string {
	output := make([]string, len(values))
	for index, value := range values {
		output[index] = string(value)
	}
	return strings.Join(output, "\x00")
}
