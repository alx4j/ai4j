package cli

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
)

type Parser struct{}

func NewParser() Parser { return Parser{} }

type parsedOptions struct {
	repository        string
	hasRepository     bool
	reference         string
	hasReference      bool
	sourcePath        string
	hasSource         bool
	expectedCommit    domain.CommitOID
	hasExpected       bool
	expectedDigest    string
	hasExpectedDigest bool
	dryRun            bool
	yes               bool
	json              bool
	allowDirty        bool
	installation      domain.InstallationID
	hasInstallation   bool
	targets           []BuildTarget
	scope             Scope
	hasScope          bool
	project           string
	hasProject        bool
	gitRoot           string
	hasGitRoot        bool
	host              BuildHost
	output            string
	all               bool
	assets            []string
	bundles           []string
	coordinates       []BundleCoordinate
	examples          bool
	conflictPolicy    string
	hasConflictPolicy bool
	operation         domain.OperationID
	hasOperation      bool
	testMCP           string
	hasTestMCP        bool
	expired           bool
}

type optionKind uint8

const (
	booleanOption optionKind = iota
	valueOption
)

var commandOptions = map[Command]map[string]optionKind{
	CommandInit:         {"target": valueOption, "output": valueOption, "examples": booleanOption, "json": booleanOption},
	CommandValidate:     {"repo": valueOption, "ref": valueOption, "source": valueOption, "target": valueOption, "allow-dirty": booleanOption, "json": booleanOption},
	CommandBuild:        {"repo": valueOption, "ref": valueOption, "source": valueOption, "target": valueOption, "host": valueOption, "output": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "json": booleanOption},
	CommandInstall:      {"repo": valueOption, "ref": valueOption, "source": valueOption, "git-root": valueOption, "installation": valueOption, "target": valueOption, "scope": valueOption, "project": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandUpdate:       {"repo": valueOption, "ref": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandSync:         {"bundle": valueOption, "allow-dirty": booleanOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandList:         {"target": valueOption, "scope": valueOption, "json": booleanOption},
	CommandStatus:       {"json": booleanOption},
	CommandDoctor:       {"test-mcp": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandRollback:     {"operation": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandUninstall:    {"conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandHistory:      {"json": booleanOption},
	CommandHistoryPurge: {"operation": valueOption, "expired": booleanOption, "all": booleanOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandVersion:      {"json": booleanOption},
}

var knownOptions = func() map[string]struct{} {
	result := map[string]struct{}{"selection": {}, "force": {}}
	for _, options := range commandOptions {
		for name := range options {
			result[name] = struct{}{}
		}
	}
	return result
}()

func isKnownOption(name string) bool {
	_, ok := knownOptions[name]
	return ok
}

func (p Parser) Parse(argv []string) (Request, error) {
	jsonRequested := containsExactJSON(argv)
	if len(argv) == 0 {
		return nil, newUsageError(UsageMissingExecutable, "", "", jsonRequested, nil)
	}
	if len(argv) == 1 {
		return nil, newUsageError(UsageMissingCommand, "", "", jsonRequested, nil)
	}
	command, optionStart, issue := parseCommand(argv[1:])
	if issue != "" {
		return nil, newUsageError(issue, "", "", jsonRequested, nil)
	}
	arguments := argv[1+optionStart:]
	installation, hasInstallation, arguments, err := parseInstallationArgument(command, arguments, jsonRequested)
	if err != nil {
		return nil, err
	}
	options, err := parseOptions(command, arguments, jsonRequested)
	if err != nil {
		return nil, err
	}
	if hasInstallation {
		options.installation = installation
		options.hasInstallation = true
	}
	if err := validateOptionRelationships(command, options, jsonRequested); err != nil {
		return nil, err
	}
	output := OutputHuman
	if options.json {
		output = OutputJSON
	}
	source := SourceOptions{repository: options.repository, repositoryProvided: options.hasRepository, reference: options.reference, referenceProvided: options.hasReference, checkout: options.sourcePath, checkoutProvided: options.hasSource, allowDirty: options.allowDirty}
	switch command {
	case CommandInit:
		if len(options.targets) == 0 {
			return nil, newUsageError(UsageMissingOptionValue, command, "target", jsonRequested, nil)
		}
		if options.output == "" {
			return nil, newUsageError(UsageMissingOptionValue, command, "output", jsonRequested, nil)
		}
		return InitRequest{targets: append([]BuildTarget(nil), options.targets...), output: options.output, examples: options.examples, mode: output}, nil
	case CommandValidate:
		return ValidateRequest{source: source, target: firstTarget(options.targets), output: output}, nil
	case CommandBuild:
		required := []struct {
			name    string
			present bool
		}{
			{name: "target", present: len(options.targets) == 1},
			{name: "host", present: options.host.Valid()},
			{name: "output", present: options.output != ""},
		}
		for _, option := range required {
			if !option.present {
				return nil, newUsageError(UsageMissingOptionValue, command, option.name, jsonRequested, nil)
			}
		}
		hasExplicitSelection := len(options.assets) != 0 || len(options.bundles) != 0
		if !options.all && !hasExplicitSelection {
			return nil, newUsageError(UsageMissingOptionValue, command, "all", jsonRequested, nil)
		}
		if options.all && hasExplicitSelection {
			return nil, newUsageError(UsageInvalidOptionValue, command, "all", jsonRequested, nil)
		}
		return BuildRequest{source: source, target: options.targets[0], host: options.host, output: options.output, all: options.all, assets: append([]string(nil), options.assets...), bundles: append([]string(nil), options.bundles...), mode: output}, nil
	case CommandInstall:
		return InstallRequest{source: source, target: firstTarget(options.targets), scope: options.scope, project: options.project, hasProject: options.hasProject, selection: bundleSelection(options), gitRoot: options.gitRoot, hasGitRoot: options.hasGitRoot, coordinates: append([]BundleCoordinate(nil), options.coordinates...), installation: options.installation, hasInstallation: options.hasInstallation, expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandUpdate:
		return UpdateRequest{installation: options.installation, source: source, policy: conflictPolicy(options), expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandSync:
		return SyncRequest{installation: options.installation, selection: bundleSelection(options), allowDirty: options.allowDirty, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, policy: conflictPolicy(options), dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandDoctor:
		return DoctorRequest{installation: options.installation, testMCP: options.testMCP, yes: options.yes, output: output}, nil
	case CommandRollback:
		return RollbackRequest{installation: options.installation, operation: options.operation, hasOperation: options.hasOperation, policy: conflictPolicy(options), dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandHistory:
		return HistoryRequest{installation: options.installation, output: output}, nil
	case CommandHistoryPurge:
		return HistoryPurgeRequest{installation: options.installation, selection: historyPurgeSelection(options), operation: options.operation, dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandList:
		return ListRequest{target: firstTarget(options.targets), hasTarget: len(options.targets) == 1, scope: options.scope, hasScope: options.hasScope, output: output}, nil
	case CommandStatus:
		return StatusRequest{installation: options.installation, output: output}, nil
	case CommandUninstall:
		return UninstallRequest{installation: options.installation, policy: conflictPolicy(options), dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandVersion:
		return VersionRequest{output: output}, nil
	default:
		panic("validated command is not handled")
	}
}

func parseCommand(arguments []string) (Command, int, UsageIssue) {
	if len(arguments) == 0 {
		return "", 0, UsageMissingCommand
	}
	if strings.HasPrefix(arguments[0], "-") {
		return "", 0, UsageMisplacedOption
	}
	switch arguments[0] {
	case "init":
		return CommandInit, 1, ""
	case "validate":
		return CommandValidate, 1, ""
	case "build":
		return CommandBuild, 1, ""
	case "install":
		return CommandInstall, 1, ""
	case "update":
		return CommandUpdate, 1, ""
	case "sync":
		return CommandSync, 1, ""
	case "status":
		return CommandStatus, 1, ""
	case "list":
		return CommandList, 1, ""
	case "doctor":
		return CommandDoctor, 1, ""
	case "rollback":
		return CommandRollback, 1, ""
	case "history":
		if len(arguments) >= 2 && arguments[1] == "purge" {
			return CommandHistoryPurge, 2, ""
		}
		return CommandHistory, 1, ""
	case "uninstall":
		return CommandUninstall, 1, ""
	case "version":
		return CommandVersion, 1, ""
	}
	return "", 0, UsageUnknownCommand
}

func parseInstallationArgument(command Command, arguments []string, jsonRequested bool) (domain.InstallationID, bool, []string, error) {
	if !usesPositionalInstallation(command) || len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return domain.InstallationID{}, false, arguments, nil
	}
	installation, err := domain.NewInstallationID(arguments[0])
	if err != nil {
		return domain.InstallationID{}, false, nil, newUsageError(UsageInvalidOptionValue, command, "installation", jsonRequested, err)
	}
	return installation, true, arguments[1:], nil
}

func usesPositionalInstallation(command Command) bool {
	switch command {
	case CommandUpdate, CommandSync, CommandStatus, CommandDoctor, CommandRollback, CommandUninstall, CommandHistory, CommandHistoryPurge:
		return true
	default:
		return false
	}
}

func parseOptions(command Command, arguments []string, jsonRequested bool) (parsedOptions, error) {
	var result parsedOptions
	seen := make(map[string]struct{}, len(arguments))
	allowed := commandOptions[command]
	for index := 0; index < len(arguments); index++ {
		token := arguments[index]
		if token == "--" || !strings.HasPrefix(token, "--") || token == "-" {
			return parsedOptions{}, newUsageError(UsageUnexpectedArgument, command, "", jsonRequested, nil)
		}
		nameValue := strings.SplitN(strings.TrimPrefix(token, "--"), "=", 2)
		name := nameValue[0]
		kind, ok := allowed[name]
		if !ok || name == "" {
			issue := UsageUnknownOption
			safeOption := ""
			if isKnownOption(name) {
				issue = UsageInapplicableOption
				safeOption = name
			}
			return parsedOptions{}, newUsageError(issue, command, safeOption, jsonRequested, nil)
		}
		repeatable := command == CommandBuild && (name == "asset" || name == "bundle") || command == CommandInstall && name == "bundle" || (command == CommandInit || command == CommandValidate) && name == "target"
		if _, duplicate := seen[name]; duplicate && !repeatable {
			return parsedOptions{}, newUsageError(UsageDuplicateOption, command, name, jsonRequested, nil)
		}
		seen[name] = struct{}{}
		if kind == booleanOption {
			if len(nameValue) == 2 {
				return parsedOptions{}, newUsageError(UsageUnexpectedOptionValue, command, name, jsonRequested, nil)
			}
			setBoolean(&result, name)
			continue
		}
		var value string
		if len(nameValue) == 2 {
			value = nameValue[1]
		} else {
			if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return parsedOptions{}, newUsageError(UsageMissingOptionValue, command, name, jsonRequested, nil)
			}
			index++
			value = arguments[index]
		}
		if value == "" {
			return parsedOptions{}, newUsageError(UsageEmptyOptionValue, command, name, jsonRequested, nil)
		}
		if err := setValue(command, &result, name, value); err != nil {
			return parsedOptions{}, newUsageError(UsageInvalidOptionValue, command, name, jsonRequested, err)
		}
	}
	return result, nil
}

func setBoolean(options *parsedOptions, name string) {
	switch name {
	case "json":
		options.json = true
	case "yes":
		options.yes = true
	case "dry-run":
		options.dryRun = true
	case "allow-dirty":
		options.allowDirty = true
	case "all":
		options.all = true
	case "examples":
		options.examples = true
	case "expired":
		options.expired = true
	}
}

func setValue(command Command, options *parsedOptions, name, value string) error {
	switch name {
	case "repo":
		options.repository = value
		options.hasRepository = true
	case "ref":
		options.reference = value
		options.hasReference = true
	case "source":
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("source path is invalid")
		}
		options.sourcePath = value
		options.hasSource = true
	case "git-root":
		if len(value) > 4096 || strings.ContainsRune(value, 0) || strings.TrimSpace(value) != value {
			return fmt.Errorf("Git root is invalid")
		}
		options.gitRoot = value
		options.hasGitRoot = true
	case "expected-commit":
		commit, err := domain.NewCommitOID(value)
		if err != nil {
			return err
		}
		options.expectedCommit = commit
		options.hasExpected = true
	case "expected-source-digest":
		if len(value) != 64 {
			return fmt.Errorf("source digest is invalid")
		}
		for _, character := range value {
			if character < '0' || character > '9' && character < 'a' || character > 'f' {
				return fmt.Errorf("source digest is invalid")
			}
		}
		options.expectedDigest = value
		options.hasExpectedDigest = true
	case "installation":
		installation, err := domain.NewInstallationID(value)
		if err != nil {
			return err
		}
		options.installation = installation
		options.hasInstallation = true
	case "target":
		target := BuildTarget(value)
		if !target.Valid() {
			return fmt.Errorf("target is unsupported")
		}
		if slices.Contains(options.targets, target) || len(options.targets) >= 2 {
			return fmt.Errorf("target is duplicated")
		}
		options.targets = append(options.targets, target)
	case "host":
		host := BuildHost(value)
		if !host.Valid() {
			return fmt.Errorf("host is unsupported")
		}
		options.host = host
	case "scope":
		scope := Scope(value)
		if !scope.Valid() {
			return fmt.Errorf("scope is unsupported")
		}
		options.scope = scope
		options.hasScope = true
	case "project":
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("project path is invalid")
		}
		options.project = value
		options.hasProject = true
	case "conflict-policy":
		if value != "fail" && value != "keep" && value != "replace-owned" && value != "interactive" {
			return fmt.Errorf("conflict policy is unsupported")
		}
		options.conflictPolicy = value
		options.hasConflictPolicy = true
	case "operation":
		operation, err := domain.NewOperationID(value)
		if err != nil {
			return err
		}
		options.operation = operation
		options.hasOperation = true
	case "test-mcp":
		if !selectionIdentifier(value) {
			return fmt.Errorf("MCP server identifier is invalid")
		}
		options.testMCP = value
		options.hasTestMCP = true
	case "output":
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("output path is invalid")
		}
		options.output = value
	case "asset":
		if !selectionIdentifier(value) || slices.Contains(options.assets, value) {
			return fmt.Errorf("asset identifier is invalid or duplicated")
		}
		options.assets = append(options.assets, value)
	case "bundle":
		if command == CommandInstall {
			coordinate, coordinateSyntax, err := parseBundleCoordinate(value)
			if err != nil {
				return err
			}
			if coordinateSyntax {
				if slices.Contains(options.coordinates, coordinate) {
					return fmt.Errorf("bundle coordinate is duplicated")
				}
				options.coordinates = append(options.coordinates, coordinate)
				return nil
			}
		}
		if !selectionIdentifier(value) || slices.Contains(options.bundles, value) {
			return fmt.Errorf("bundle identifier is invalid or duplicated")
		}
		options.bundles = append(options.bundles, value)
	default:
		return fmt.Errorf("unhandled value option")
	}
	return nil
}

func parseBundleCoordinate(value string) (BundleCoordinate, bool, error) {
	name, tag, coordinateSyntax := strings.Cut(value, "@")
	if !coordinateSyntax {
		return BundleCoordinate{}, false, nil
	}
	coordinate, err := NewBundleCoordinate(name, tag)
	if err != nil {
		return BundleCoordinate{}, true, err
	}
	return coordinate, true, nil
}

func firstTarget(values []BuildTarget) BuildTarget {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func bundleSelection(options parsedOptions) BundleSelection {
	if len(options.bundles) == 0 {
		return BundleSelection{}
	}
	return BundleSelection{bundle: options.bundles[0]}
}

func conflictPolicy(options parsedOptions) ConflictPolicy {
	if !options.hasConflictPolicy {
		return ConflictFail
	}
	return ConflictPolicy(options.conflictPolicy)
}

func historyPurgeSelection(options parsedOptions) HistoryPurgeSelection {
	if options.hasOperation {
		return HistoryPurgeOperation
	}
	if options.expired {
		return HistoryPurgeExpired
	}
	return HistoryPurgeAll
}

func validateOptionRelationships(command Command, options parsedOptions, jsonRequested bool) error {
	if err := validateCommonOptionRelationships(command, options, jsonRequested); err != nil {
		return err
	}
	return validateCommandOptionRelationships(command, options, jsonRequested)
}

func validateCommonOptionRelationships(command Command, options parsedOptions, jsonRequested bool) error {
	if command == CommandInstall && options.hasGitRoot &&
		(options.hasRepository || options.hasReference || options.hasSource || options.allowDirty || options.hasExpected || options.hasExpectedDigest || options.hasInstallation) {
		return invalidOption(command, "git-root", jsonRequested)
	}
	if options.hasSource && (options.hasRepository || options.hasReference) {
		return invalidOption(command, "source", jsonRequested)
	}
	if options.hasExpected && options.hasExpectedDigest {
		return invalidOption(command, "expected-source-digest", jsonRequested)
	}
	if options.hasSource && options.hasExpected {
		return invalidOption(command, "expected-commit", jsonRequested)
	}
	if options.hasExpectedDigest && command == CommandInstall && !options.hasSource {
		return invalidOption(command, "expected-source-digest", jsonRequested)
	}
	if options.dryRun && options.yes {
		return invalidOption(command, "yes", jsonRequested)
	}
	if options.dryRun && options.hasExpected {
		return invalidOption(command, "expected-commit", jsonRequested)
	}
	if options.dryRun && options.hasExpectedDigest {
		return invalidOption(command, "expected-source-digest", jsonRequested)
	}
	if options.hasConflictPolicy && options.conflictPolicy == "interactive" && (jsonRequested || options.dryRun) {
		return invalidOption(command, "conflict-policy", jsonRequested)
	}
	if options.hasProject && (!options.hasScope || options.scope == ScopeUser) {
		return invalidOption(command, "project", jsonRequested)
	}
	if options.all && (len(options.assets) != 0 || len(options.bundles) != 0) {
		return invalidOption(command, "all", jsonRequested)
	}
	return nil
}

func validateCommandOptionRelationships(command Command, options parsedOptions, jsonRequested bool) error {
	hasSelection := options.all || len(options.assets) != 0 || len(options.bundles) != 0 || len(options.coordinates) != 0
	switch command {
	case CommandValidate:
		if len(options.targets) == 0 {
			return missingOption(command, "target", jsonRequested)
		}
		if len(options.targets) > 1 {
			return invalidOption(command, "target", jsonRequested)
		}
		if options.allowDirty && !options.hasSource {
			return invalidOption(command, "allow-dirty", jsonRequested)
		}
	case CommandBuild:
		if options.allowDirty && !options.hasSource {
			return invalidOption(command, "allow-dirty", jsonRequested)
		}
	case CommandInstall:
		if options.hasInstallation {
			if options.hasRepository || options.hasReference || options.hasSource || len(options.targets) != 0 || options.hasScope || options.hasProject || hasSelection || options.hasExpected || options.hasExpectedDigest {
				return invalidOption(command, "installation", jsonRequested)
			}
			return nil
		}
		if len(options.targets) == 0 {
			return missingOption(command, "target", jsonRequested)
		}
		if !options.hasScope {
			return missingOption(command, "scope", jsonRequested)
		}
		if options.hasGitRoot {
			if len(options.bundles) != 0 || len(options.coordinates) < 2 || len(options.coordinates) > 3 || !uniqueCoordinateNames(options.coordinates) {
				return invalidOption(command, "bundle", jsonRequested)
			}
			break
		}
		if len(options.coordinates) != 0 {
			return missingOption(command, "git-root", jsonRequested)
		}
		if len(options.bundles) == 0 {
			return missingOption(command, "bundle", jsonRequested)
		}
		if len(options.bundles) != 1 {
			return invalidOption(command, "bundle", jsonRequested)
		}
		if options.allowDirty && !options.hasSource {
			return invalidOption(command, "allow-dirty", jsonRequested)
		}
	case CommandUpdate:
		if !options.hasInstallation {
			return missingOption(command, "installation", jsonRequested)
		}
	case CommandSync:
		if !options.hasInstallation {
			return missingOption(command, "installation", jsonRequested)
		}
		if len(options.bundles) != 1 {
			return missingOption(command, "bundle", jsonRequested)
		}
	case CommandStatus, CommandDoctor:
		if !options.hasInstallation {
			return missingOption(command, "installation", jsonRequested)
		}
	case CommandRollback, CommandUninstall, CommandHistory:
		if !options.hasInstallation {
			return missingOption(command, "installation", jsonRequested)
		}
	case CommandHistoryPurge:
		if !options.hasInstallation {
			return missingOption(command, "installation", jsonRequested)
		}
		choices := 0
		if options.hasOperation {
			choices++
		}
		if options.expired {
			choices++
		}
		if options.all {
			choices++
		}
		if choices != 1 {
			return invalidOption(command, "all", jsonRequested)
		}
	}
	return nil
}

func invalidOption(command Command, option string, jsonRequested bool) error {
	return newUsageError(UsageInvalidOptionValue, command, option, jsonRequested, nil)
}

func missingOption(command Command, option string, jsonRequested bool) error {
	return newUsageError(UsageMissingOptionValue, command, option, jsonRequested, nil)
}

func selectionIdentifier(value string) bool {
	if len(value) < 2 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
			return false
		}
	}
	return true
}

func uniqueCoordinateNames(coordinates []BundleCoordinate) bool {
	seen := make(map[string]struct{}, len(coordinates))
	for _, coordinate := range coordinates {
		if _, exists := seen[coordinate.Name()]; exists {
			return false
		}
		seen[coordinate.Name()] = struct{}{}
	}
	return true
}

func bundleTag(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.TrimSpace(value) != value ||
		strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "refs/") ||
		strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") ||
		strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") ||
		value == "HEAD" || value == "@" {
		return false
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func containsExactJSON(arguments []string) bool {
	return slices.Contains(arguments, "--json")
}
