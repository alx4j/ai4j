package validate

type toolkitManifestV2 struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Toolkit       toolkitIdentityV2   `json:"toolkit"`
	Assets        []assetV2           `json:"assets"`
	Bundles       []bundleV2          `json:"bundles"`
	Targets       map[string]targetV2 `json:"targets"`
}

type toolkitIdentityV2 struct {
	ID            string          `json:"id"`
	Version       string          `json:"version"`
	DisplayName   string          `json:"displayName"`
	Description   string          `json:"description,omitempty"`
	DeclarationID string          `json:"declarationId,omitempty"`
	Compatibility compatibilityV2 `json:"compatibility"`
}

type compatibilityV2 struct {
	MinimumCLI string `json:"minimumCli"`
}

type assetV2 struct {
	ID           string                     `json:"id"`
	Type         string                     `json:"type"`
	Path         string                     `json:"path,omitempty"`
	Ownership    string                     `json:"ownership"`
	Dependencies []string                   `json:"dependencies,omitempty"`
	Variants     []assetVariantV2           `json:"variants,omitempty"`
	Executable   *executableV2              `json:"executable,omitempty"`
	Overlays     map[string]targetOverlayV2 `json:"overlays,omitempty"`
}

type executableV2 struct {
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Dependency  string   `json:"dependency"`
	Environment []string `json:"environment,omitempty"`
}

type targetOverlayV2 struct {
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Environment []string `json:"environment,omitempty"`
	HookEvents  []string `json:"hookEvents,omitempty"`
}

type assetVariantV2 struct {
	ID         string        `json:"id"`
	Path       string        `json:"path"`
	Targets    []string      `json:"targets"`
	Hosts      []string      `json:"hosts"`
	Executable *executableV2 `json:"executable,omitempty"`
}

type bundleV2 struct {
	ID      string   `json:"id"`
	Assets  []string `json:"assets,omitempty"`
	Bundles []string `json:"bundles,omitempty"`
}

type targetV2 struct {
	Packages []nativePackageV2 `json:"packages"`
}

type nativePackageV2 struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	Assets []string `json:"assets,omitempty"`
}
