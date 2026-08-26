package cli

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
)

type Parser struct{ windows bool }

func NewParser(goos string) Parser { return Parser{windows: goos == "windows"} }

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
	host              BuildHost
	output            string
	all               bool
	assets            []string
	bundles           []string
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
	CommandInstall:      {"repo": valueOption, "ref": valueOption, "source": valueOption, "installation": valueOption, "target": valueOption, "scope": valueOption, "project": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandUpdate:       {"repo": valueOption, "ref": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandSync:         {"all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandList:         {"target": valueOption, "scope": valueOption, "json": booleanOption},
	CommandStatus:       {"json": booleanOption},
	CommandDoctor:       {"test-mcp": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandRollback:     {"operation": valueOption, "conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandUninstall:    {"conflict-policy": valueOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandHistory:      {"json": booleanOption},
	CommandHistoryPurge: {"operation": valueOption, "expired": booleanOption, "all": booleanOption, "dry-run": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandVersion:      {"json": booleanOption},
}

var knownOptions = map[string]struct{}{
	"repo": {}, "ref": {}, "source": {}, "expected-commit": {}, "expected-source-digest": {}, "yes": {}, "json": {}, "allow-dirty": {},
	"target": {}, "host": {}, "output": {}, "all": {}, "asset": {}, "bundle": {}, "examples": {}, "scope": {}, "project": {}, "installation": {}, "conflict-policy": {}, "operation": {}, "test-mcp": {}, "expired": {}, "selection": {}, "force": {}, "dry-run": {},
}

func isKnownOption(name string) bool {
	_, ok := knownOptions[name]
	return ok
}

func (p Parser) Parse(argv []string) (Request, error) {
	jsonRequested := containsExactJSON(argv)
	if len(argv) == 0 {
		return nil, newUsageError(UsageMissingExecutable, "", "", jsonRequested, "cli.executable", fault.ReasonEmpty, nil)
	}
	if !p.validExecutable(argv[0]) {
		return nil, newUsageError(UsageAlternateExecutable, "", "", jsonRequested, "cli.executable", fault.ReasonUnknownValue, nil)
	}
	if len(argv) == 1 {
		return nil, newUsageError(UsageMissingCommand, "", "", jsonRequested, "cli.command", fault.ReasonEmpty, nil)
	}
	command, optionStart, issue := parseCommand(argv[1:])
	if issue != "" {
		return nil, newUsageError(issue, "", "", jsonRequested, "cli.command", fault.ReasonUnknownValue, nil)
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
			return nil, newUsageError(UsageMissingOptionValue, command, "target", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if options.output == "" {
			return nil, newUsageError(UsageMissingOptionValue, command, "output", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
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
				return nil, newUsageError(UsageMissingOptionValue, command, option.name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
			}
		}
		hasExplicitSelection := len(options.assets) != 0 || len(options.bundles) != 0
		if !options.all && !hasExplicitSelection {
			return nil, newUsageError(UsageMissingOptionValue, command, "all", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if options.all && hasExplicitSelection {
			return nil, newUsageError(UsageInvalidOptionValue, command, "all", jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
		}
		return BuildRequest{source: source, target: options.targets[0], host: options.host, output: options.output, all: options.all, assets: append([]string(nil), options.assets...), bundles: append([]string(nil), options.bundles...), mode: output}, nil
	case CommandInstall:
		return InstallRequest{source: source, target: firstTarget(options.targets), scope: options.scope, project: options.project, hasProject: options.hasProject, selection: selectionOptions(options), installation: options.installation, hasInstallation: options.hasInstallation, expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandUpdate:
		return UpdateRequest{installation: options.installation, source: source, policy: conflictPolicy(options), expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, dryRun: options.dryRun, yes: options.yes, output: output}, nil
	case CommandSync:
		return SyncRequest{installation: options.installation, selection: selectionOptions(options), allowDirty: options.allowDirty, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, policy: conflictPolicy(options), dryRun: options.dryRun, yes: options.yes, output: output}, nil
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

func (p Parser) validExecutable(value string) bool {
	if p.windows {
		base := path.Base(strings.ReplaceAll(value, `\`, "/"))
		return strings.EqualFold(base, "ai4j.exe")
	}
	return path.Base(value) == "ai4j"
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
		return domain.InstallationID{}, false, nil, newUsageError(UsageInvalidOptionValue, command, "installation", jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, err)
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
			return parsedOptions{}, newUsageError(UsageUnexpectedArgument, command, "", jsonRequested, "cli.argument", fault.ReasonInvalidFormat, nil)
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
			return parsedOptions{}, newUsageError(issue, command, safeOption, jsonRequested, "cli.option", fault.ReasonUnknownValue, nil)
		}
		repeatable := name == "asset" || name == "bundle" || (command == CommandInit || command == CommandValidate) && name == "target"
		if _, duplicate := seen[name]; duplicate && !repeatable {
			return parsedOptions{}, newUsageError(UsageDuplicateOption, command, name, jsonRequested, "cli.option", fault.ReasonInvalidFormat, nil)
		}
		seen[name] = struct{}{}
		if kind == booleanOption {
			if len(nameValue) == 2 {
				return parsedOptions{}, newUsageError(UsageUnexpectedOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
			}
			setBoolean(&result, name)
			continue
		}
		var value string
		if len(nameValue) == 2 {
			value = nameValue[1]
		} else {
			if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return parsedOptions{}, newUsageError(UsageMissingOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
			}
			index++
			value = arguments[index]
		}
		if value == "" {
			return parsedOptions{}, newUsageError(UsageEmptyOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if err := setValue(&result, name, value); err != nil {
			return parsedOptions{}, newUsageError(UsageInvalidOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, err)
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

func setValue(options *parsedOptions, name, value string) error {
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
		if !selectionIdentifier(value) || slices.Contains(options.bundles, value) {
			return fmt.Errorf("bundle identifier is invalid or duplicated")
		}
		options.bundles = append(options.bundles, value)
	default:
		return fmt.Errorf("unhandled value option")
	}
	return nil
}

func firstTarget(values []BuildTarget) BuildTarget {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func selectionOptions(options parsedOptions) SelectionOptions {
	return SelectionOptions{all: options.all, assets: append([]string(nil), options.assets...), bundles: append([]string(nil), options.bundles...)}
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
	invalid := func(option string) error {
		return newUsageError(UsageInvalidOptionValue, command, option, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
	}
	missing := func(option string) error {
		return newUsageError(UsageMissingOptionValue, command, option, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
	}
	if options.hasSource && (options.hasRepository || options.hasReference) {
		return invalid("source")
	}
	if options.hasExpected && options.hasExpectedDigest {
		return invalid("expected-source-digest")
	}
	if options.hasSource && options.hasExpected {
		return invalid("expected-commit")
	}
	if options.hasExpectedDigest && command == CommandInstall && !options.hasSource {
		return invalid("expected-source-digest")
	}
	if options.dryRun && options.yes {
		return invalid("yes")
	}
	if options.dryRun && options.hasExpected {
		return invalid("expected-commit")
	}
	if options.dryRun && options.hasExpectedDigest {
		return invalid("expected-source-digest")
	}
	if options.hasConflictPolicy && options.conflictPolicy == "interactive" && (jsonRequested || options.dryRun) {
		return invalid("conflict-policy")
	}
	if options.hasProject && (!options.hasScope || options.scope == ScopeUser) {
		return invalid("project")
	}
	hasSelection := options.all || len(options.assets) != 0 || len(options.bundles) != 0
	if options.all && (len(options.assets) != 0 || len(options.bundles) != 0) {
		return invalid("all")
	}
	switch command {
	case CommandValidate:
		if len(options.targets) == 0 {
			return missing("target")
		}
		if len(options.targets) > 1 {
			return invalid("target")
		}
		if options.allowDirty && !options.hasSource {
			return invalid("allow-dirty")
		}
	case CommandBuild:
		if options.allowDirty && !options.hasSource {
			return invalid("allow-dirty")
		}
	case CommandInstall:
		if options.hasInstallation {
			if options.hasRepository || options.hasReference || options.hasSource || len(options.targets) != 0 || options.hasScope || options.hasProject || hasSelection || options.hasExpected || options.hasExpectedDigest {
				return invalid("installation")
			}
			return nil
		}
		if len(options.targets) == 0 {
			return missing("target")
		}
		if !options.hasScope {
			return missing("scope")
		}
		if !hasSelection {
			return missing("all")
		}
		if options.allowDirty && !options.hasSource {
			return invalid("allow-dirty")
		}
	case CommandUpdate:
		if !options.hasInstallation {
			return missing("installation")
		}
	case CommandSync:
		if !options.hasInstallation {
			return missing("installation")
		}
		if !hasSelection {
			return missing("all")
		}
	case CommandStatus, CommandDoctor:
		if !options.hasInstallation {
			return missing("installation")
		}
	case CommandRollback, CommandUninstall, CommandHistory:
		if !options.hasInstallation {
			return missing("installation")
		}
	case CommandHistoryPurge:
		if !options.hasInstallation {
			return missing("installation")
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
			return invalid("all")
		}
	}
	return nil
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

func containsExactJSON(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--json" {
			return true
		}
	}
	return false
}
