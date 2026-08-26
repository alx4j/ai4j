package validate

type toolkitManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Toolkit       toolkitIdentity   `json:"toolkit"`
	Assets        []asset           `json:"assets"`
	Bundles       []bundle          `json:"bundles"`
	Targets       map[string]target `json:"targets"`
}

type toolkitIdentity struct {
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	DisplayName   string        `json:"displayName"`
	Description   string        `json:"description,omitempty"`
	DeclarationID string        `json:"declarationId,omitempty"`
	Compatibility compatibility `json:"compatibility"`
}

type compatibility struct {
	MinimumCLI string `json:"minimumCli"`
}

type asset struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`
	Path         string                   `json:"path,omitempty"`
	Ownership    string                   `json:"ownership"`
	Dependencies []string                 `json:"dependencies,omitempty"`
	Variants     []assetVariant           `json:"variants,omitempty"`
	Executable   *executable              `json:"executable,omitempty"`
	Overlays     map[string]targetOverlay `json:"overlays,omitempty"`
}

type executable struct {
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Dependency  string   `json:"dependency"`
	Environment []string `json:"environment,omitempty"`
}

type targetOverlay struct {
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Environment []string `json:"environment,omitempty"`
	HookEvents  []string `json:"hookEvents,omitempty"`
}

type assetVariant struct {
	ID         string      `json:"id"`
	Path       string      `json:"path"`
	Targets    []string    `json:"targets"`
	Hosts      []string    `json:"hosts"`
	Executable *executable `json:"executable,omitempty"`
}

type bundle struct {
	ID       string   `json:"id"`
	Assets   []string `json:"assets,omitempty"`
	Packages []string `json:"packages,omitempty"`
	Bundles  []string `json:"bundles,omitempty"`
}

type target struct {
	Packages []nativePackage `json:"packages"`
}

type nativePackage struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	Assets []string `json:"assets,omitempty"`
}
