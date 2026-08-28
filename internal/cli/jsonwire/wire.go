// Package jsonwire owns stable JSON wire objects. DTOs are concrete:
// they contain no maps, interface fields, raw JSON, or Go errors.
package jsonwire

type UsageData struct{}

type Envelope[D any] struct {
	SchemaVersion int          `json:"schemaVersion"`
	Command       *string      `json:"command"`
	Status        string       `json:"status"`
	Changed       bool         `json:"changed"`
	OperationID   *string      `json:"operationId"`
	ExitCode      int          `json:"exitCode"`
	Data          D            `json:"data"`
	Warnings      []Diagnostic `json:"warnings"`
	Errors        []Diagnostic `json:"errors"`
}

type Diagnostic struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Context []Context `json:"context"`
}

type Context struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type SourceCommit struct {
	ObjectFormat string `json:"objectFormat"`
	OID          string `json:"oid"`
}

type SourceTree struct {
	ObjectFormat string `json:"objectFormat"`
	OID          string `json:"oid"`
}

type SourceRenderedDigest struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

type SourceBuildCommit struct {
	ObjectFormat string `json:"objectFormat"`
	OID          string `json:"oid"`
}

type Source struct {
	SourceMode      string                `json:"sourceMode"`
	SourceSelection string                `json:"sourceSelection"`
	Repository      *string               `json:"repository"`
	Transport       *string               `json:"transport"`
	RequestedRef    *string               `json:"requestedRef"`
	ResolvedRefKind *string               `json:"resolvedRefKind"`
	ResolvedRefName *string               `json:"resolvedRefName"`
	TrackingPolicy  *string               `json:"trackingPolicy"`
	Commit          *SourceCommit         `json:"commit"`
	RootTree        *SourceTree           `json:"rootTree"`
	Checkout        *string               `json:"checkout"`
	SourceDigest    *SourceRenderedDigest `json:"sourceDigest"`
	Dirty           bool                  `json:"dirty"`
	RenderedDigest  SourceRenderedDigest  `json:"renderedDigest"`
	CLIBuildCommit  SourceBuildCommit     `json:"cliBuildCommit"`
}

type Validation struct {
	Valid        bool `json:"valid"`
	ErrorCount   int  `json:"errorCount"`
	WarningCount int  `json:"warningCount"`
}

type ContentItem struct {
	ComponentType string     `json:"componentType"`
	Identifier    string     `json:"identifier"`
	SourcePath    string     `json:"sourcePath"`
	Checksum      Checksum   `json:"checksum"`
	Change        string     `json:"change"`
	Execution     *Execution `json:"execution"`
}

type Checksum struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}
type Execution struct {
	Ownership             string   `json:"ownership"`
	Dependency            string   `json:"dependency"`
	Command               string   `json:"command"`
	Args                  []string `json:"args"`
	CWD                   *string  `json:"cwd"`
	SupportedPlaceholders []string `json:"supportedPlaceholders"`
	Environment           []string `json:"environment"`
}

type ValidateData struct {
	Source        Source        `json:"source"`
	Validation    Validation    `json:"validation"`
	ActiveContent []ContentItem `json:"activeContent"`
}

type BuildArtifact struct {
	Path      string   `json:"path"`
	Checksum  Checksum `json:"checksum"`
	SizeBytes uint64   `json:"sizeBytes"`
}

type InitData struct {
	Targets      []string        `json:"targets"`
	OutputRoot   string          `json:"outputRoot"`
	Validation   Validation      `json:"validation"`
	Reproducible bool            `json:"reproducible"`
	Artifacts    []BuildArtifact `json:"artifacts"`
}

type BuildSelection struct {
	Asset       string `json:"asset"`
	Variant     string `json:"variant"`
	Reason      string `json:"reason"`
	RequestedBy string `json:"requestedBy"`
}

type BuildData struct {
	Source        Source           `json:"source"`
	Target        string           `json:"target"`
	Host          string           `json:"host"`
	OutputRoot    string           `json:"outputRoot"`
	Reproducible  bool             `json:"reproducible"`
	Validation    Validation       `json:"validation"`
	Artifacts     []BuildArtifact  `json:"artifacts"`
	Selection     []BuildSelection `json:"selection"`
	ActiveContent []ContentItem    `json:"activeContent"`
}

type Action struct {
	Sequence              int       `json:"sequence"`
	Owner                 string    `json:"owner"`
	Kind                  string    `json:"kind"`
	Resource              string    `json:"resource"`
	ExpectedPrecondition  Condition `json:"expectedPrecondition"`
	ProposedPostcondition Condition `json:"proposedPostcondition"`
	RecoveryRequirement   string    `json:"recoveryRequirement"`
}

type Condition struct {
	State    string    `json:"state"`
	Checksum *Checksum `json:"checksum"`
}

type Conflict struct {
	Code     string `json:"code"`
	Resource string `json:"resource"`
	Message  string `json:"message"`
}

type FinalState struct {
	Installation string `json:"installation"`
	Native       string `json:"native"`
	OwnedState   string `json:"ownedState"`
}

type PlanData struct {
	Operation          string          `json:"operation"`
	Source             *Source         `json:"source"`
	Components         []PlanComponent `json:"components,omitempty"`
	InstallationID     string          `json:"installationId"`
	Actions            []Action        `json:"actions"`
	ActiveContent      []ContentItem   `json:"activeContent"`
	Conflicts          []Conflict      `json:"conflicts"`
	ExpectedFinalState FinalState      `json:"expectedFinalState"`
	UpdateDisposition  string          `json:"updateDisposition"`
}

type PlanComponent struct {
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	Source Source `json:"source"`
}

type OperationResult struct {
	Phase         string `json:"phase"`
	Outcome       string `json:"outcome"`
	Mutation      string `json:"mutation"`
	DurableChange string `json:"durableChange"`
}

type MutationData struct {
	Operation         string          `json:"operation"`
	OperationResult   OperationResult `json:"operationResult"`
	InstallationID    *string         `json:"installationId"`
	AppliedActions    []Action        `json:"appliedActions"`
	FinalState        FinalState      `json:"finalState"`
	UpdateDisposition string          `json:"updateDisposition"`
}

type HistoryDescriptor struct {
	OperationID string `json:"operationId"`
	Operation   string `json:"operation"`
	Timestamp   string `json:"timestamp"`
	Restorable  bool   `json:"restorable"`
}

type HistoryData struct {
	InstallationID string              `json:"installationId"`
	Entries        []HistoryDescriptor `json:"entries"`
}

type Installation struct {
	InstallationID  string              `json:"installationId"`
	ToolkitID       string              `json:"toolkitId"`
	NativePluginIDs []string            `json:"nativePluginIds"`
	Source          RecordedSource      `json:"source"`
	Components      []RecordedComponent `json:"components,omitempty"`
	ToolkitVersion  string              `json:"toolkitVersion"`
	CLIVersion      string              `json:"cliVersion"`
}

type RecordedSource struct {
	SourceMode      string                `json:"sourceMode"`
	SourceSelection string                `json:"sourceSelection"`
	Repository      *string               `json:"repository"`
	Transport       *string               `json:"transport"`
	RequestedRef    *string               `json:"requestedRef"`
	ResolvedRefKind *string               `json:"resolvedRefKind"`
	Commit          *SourceCommit         `json:"commit"`
	Checkout        *string               `json:"checkout"`
	SourceDigest    *SourceRenderedDigest `json:"sourceDigest"`
	Dirty           bool                  `json:"dirty"`
}

type RecordedComponent struct {
	Name            string         `json:"name"`
	Tag             string         `json:"tag"`
	Source          RecordedSource `json:"source"`
	ToolkitVersion  string         `json:"toolkitVersion"`
	ResolvedBundles []string       `json:"resolvedBundles"`
	Packages        []string       `json:"packages"`
	ResolvedAssets  []string       `json:"resolvedAssets"`
}

type NativeState struct {
	Registration string        `json:"registration"`
	Installation string        `json:"installation"`
	Enablement   string        `json:"enablement"`
	Activation   string        `json:"activation"`
	Reload       string        `json:"reload"`
	NextSession  string        `json:"nextSession"`
	Policy       string        `json:"policy"`
	Version      NativeVersion `json:"version"`
}

type NativeVersion struct {
	Expected    *string `json:"expected"`
	Observed    *string `json:"observed"`
	Observation string  `json:"observation"`
}

type Drift struct {
	Resource string `json:"resource"`
	State    string `json:"state"`
}

type RecoveryState struct {
	State string  `json:"state"`
	Phase *string `json:"phase"`
}

type StatusData struct {
	Installation      *Installation        `json:"installation"`
	Summary           *InstallationSummary `json:"summary,omitempty"`
	NativeState       NativeState          `json:"nativeState"`
	Drift             []Drift              `json:"drift"`
	RecoveryState     RecoveryState        `json:"recoveryState"`
	UpdateDisposition string               `json:"updateDisposition"`
}

type DoctorCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type MCPStartupCheck struct {
	ServerID         string   `json:"serverId"`
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	Environment      []string `json:"environment"`
	WorkingDirectory string   `json:"workingDirectory"`
	Ownership        string   `json:"ownership"`
	Result           string   `json:"result"`
	ExitCode         *int     `json:"exitCode"`
}

type DoctorData struct {
	InstallationID string           `json:"installationId"`
	Checks         []DoctorCheck    `json:"checks"`
	StartupCheck   *MCPStartupCheck `json:"startupCheck"`
}

type InstallationSummary struct {
	InstallationID  string              `json:"installationId"`
	ToolkitID       string              `json:"toolkitId"`
	Target          string              `json:"target"`
	Scope           string              `json:"scope"`
	ScopeRoot       string              `json:"scopeRoot"`
	Lifecycle       string              `json:"lifecycle"`
	Source          RecordedSource      `json:"source"`
	Components      []RecordedComponent `json:"components,omitempty"`
	RequestedBundle string              `json:"requestedBundle"`
	ResolvedBundles []string            `json:"resolvedBundles"`
	Packages        []string            `json:"packages"`
	ResolvedAssets  []string            `json:"resolvedAssets"`
	Health          string              `json:"health"`
	HistoryCount    int                 `json:"historyCount"`
	LastOperationID *string             `json:"lastOperationId"`
}

type ListData struct {
	Installations []InstallationSummary `json:"installations"`
}

type BuildCommit struct {
	Repository   string `json:"repository"`
	ObjectFormat string `json:"objectFormat"`
	OID          string `json:"oid"`
}

type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type DefaultSource struct {
	Repository string  `json:"repository"`
	Reference  *string `json:"reference"`
	RefPolicy  string  `json:"refPolicy"`
}

type VersionData struct {
	Product       string        `json:"product"`
	Executable    string        `json:"executable"`
	CLIVersion    string        `json:"cliVersion"`
	CLISource     BuildCommit   `json:"cliSource"`
	GoVersion     string        `json:"goVersion"`
	BuildTime     string        `json:"buildTime"`
	Target        Target        `json:"target"`
	DefaultSource DefaultSource `json:"defaultSource"`
}
