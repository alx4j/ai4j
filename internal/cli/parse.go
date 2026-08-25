package cli

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/fault"
)

const codexNativeLifecycleUnavailable = "Codex exposes plugin lifecycle only through its interactive native plugin browser; build the Codex package with ai4j, then install or manage it through /plugins or the Codex desktop plugin browser"

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
	yes               bool
	json              bool
	allowDirty        bool
	checkUpdates      bool
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
	CommandInit:             {"target": valueOption, "output": valueOption, "examples": booleanOption, "json": booleanOption},
	CommandValidate:         {"repo": valueOption, "ref": valueOption, "source": valueOption, "target": valueOption, "allow-dirty": booleanOption, "json": booleanOption},
	CommandBuild:            {"repo": valueOption, "ref": valueOption, "source": valueOption, "target": valueOption, "host": valueOption, "output": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "json": booleanOption},
	CommandPlanInstall:      {"repo": valueOption, "ref": valueOption, "source": valueOption, "installation": valueOption, "target": valueOption, "scope": valueOption, "project": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "json": booleanOption},
	CommandInstall:          {"repo": valueOption, "ref": valueOption, "source": valueOption, "installation": valueOption, "target": valueOption, "scope": valueOption, "project": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandPlanUpdate:       {"installation": valueOption, "repo": valueOption, "ref": valueOption, "allow-dirty": booleanOption, "conflict-policy": valueOption, "json": booleanOption},
	CommandUpdate:           {"installation": valueOption, "repo": valueOption, "ref": valueOption, "allow-dirty": booleanOption, "expected-commit": valueOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandPlanSync:         {"installation": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "conflict-policy": valueOption, "json": booleanOption},
	CommandSync:             {"installation": valueOption, "all": booleanOption, "asset": valueOption, "bundle": valueOption, "allow-dirty": booleanOption, "expected-source-digest": valueOption, "conflict-policy": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandList:             {"target": valueOption, "scope": valueOption, "json": booleanOption},
	CommandStatus:           {"installation": valueOption, "check-updates": booleanOption, "json": booleanOption},
	CommandDoctor:           {"installation": valueOption, "test-mcp": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandPlanRollback:     {"installation": valueOption, "operation": valueOption, "conflict-policy": valueOption, "json": booleanOption},
	CommandRollback:         {"installation": valueOption, "operation": valueOption, "conflict-policy": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandPlanUninstall:    {"installation": valueOption, "conflict-policy": valueOption, "json": booleanOption},
	CommandUninstall:        {"installation": valueOption, "conflict-policy": valueOption, "yes": booleanOption, "json": booleanOption},
	CommandHistory:          {"installation": valueOption, "json": booleanOption},
	CommandPlanHistoryPurge: {"installation": valueOption, "operation": valueOption, "expired": booleanOption, "all": booleanOption, "json": booleanOption},
	CommandHistoryPurge:     {"installation": valueOption, "operation": valueOption, "expired": booleanOption, "all": booleanOption, "yes": booleanOption, "json": booleanOption},
	CommandVersion:          {"json": booleanOption},
}

var knownOptions = map[string]struct{}{
	"repo": {}, "ref": {}, "source": {}, "expected-commit": {}, "expected-source-digest": {}, "yes": {}, "json": {}, "allow-dirty": {}, "check-updates": {},
	"target": {}, "host": {}, "output": {}, "all": {}, "asset": {}, "bundle": {}, "examples": {}, "scope": {}, "project": {}, "installation": {}, "conflict-policy": {}, "operation": {}, "test-mcp": {}, "expired": {}, "selection": {}, "force": {}, "dry-run": {},
}

func (p Parser) Parse(argv []string) (Request, error) {
	jsonRequested := containsExactJSON(argv)
	if len(argv) == 0 {
		return nil, usage(UsageMissingExecutable, "", "", jsonRequested, "cli.executable", fault.ReasonEmpty, nil)
	}
	if !p.validExecutable(argv[0]) {
		return nil, usage(UsageAlternateExecutable, "", "", jsonRequested, "cli.executable", fault.ReasonUnknownValue, nil)
	}
	if len(argv) == 1 {
		return nil, usage(UsageMissingCommand, "", "", jsonRequested, "cli.command", fault.ReasonEmpty, nil)
	}
	command, optionStart, issue := parseCommand(argv[1:])
	if issue != "" {
		return nil, usage(issue, "", "", jsonRequested, "cli.command", fault.ReasonUnknownValue, nil)
	}
	options, err := parseOptions(command, argv[1+optionStart:], jsonRequested)
	if err != nil {
		return nil, err
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
			return nil, usage(UsageMissingOptionValue, command, "target", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if options.output == "" {
			return nil, usage(UsageMissingOptionValue, command, "output", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		return InitRequest{targets: append([]BuildTarget(nil), options.targets...), output: options.output, examples: options.examples, mode: output}, nil
	case CommandValidate:
		if len(options.targets) == 1 && options.targets[0] != BuildTargetClaude {
			return UnsupportedRequest{command: command, output: output, message: codexNativeLifecycleUnavailable}, nil
		}
		return ValidateRequest{source: source, output: output}, nil
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
				return nil, usage(UsageMissingOptionValue, command, option.name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
			}
		}
		hasExplicitSelection := len(options.assets) != 0 || len(options.bundles) != 0
		if !options.all && !hasExplicitSelection {
			return nil, usage(UsageMissingOptionValue, command, "all", jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if options.all && hasExplicitSelection {
			return nil, usage(UsageInvalidOptionValue, command, "all", jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
		}
		return BuildRequest{source: source, target: options.targets[0], host: options.host, output: options.output, all: options.all, assets: append([]string(nil), options.assets...), bundles: append([]string(nil), options.bundles...), mode: output}, nil
	case CommandPlanInstall:
		if usesV1InstallOptions(options) {
			if len(options.targets) == 1 && options.targets[0] != BuildTargetClaude {
				return UnsupportedRequest{command: command, output: output, message: codexNativeLifecycleUnavailable}, nil
			}
			return PlanInstallRequest{source: source, target: firstTarget(options.targets), scope: options.scope, project: options.project, hasProject: options.hasProject, selection: selectionOptions(options), installation: options.installation, hasInstallation: options.hasInstallation, v1: true, output: output}, nil
		}
		return PlanInstallRequest{source: source, output: output}, nil
	case CommandInstall:
		if usesV1InstallOptions(options) || options.hasExpectedDigest {
			if len(options.targets) == 1 && options.targets[0] != BuildTargetClaude {
				return UnsupportedRequest{command: command, output: output, message: codexNativeLifecycleUnavailable}, nil
			}
			return InstallRequest{source: source, target: firstTarget(options.targets), scope: options.scope, project: options.project, hasProject: options.hasProject, selection: selectionOptions(options), installation: options.installation, hasInstallation: options.hasInstallation, v1: true, expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, yes: options.yes, output: output}, nil
		}
		return InstallRequest{source: source, expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, yes: options.yes, output: output}, nil
	case CommandPlanUpdate:
		if options.hasInstallation || options.hasRepository || options.hasReference || options.allowDirty || options.hasConflictPolicy {
			return PlanUpdateRequest{installation: options.installation, hasInstallation: options.hasInstallation, source: source, policy: conflictPolicy(options), v1: true, output: output}, nil
		}
		return PlanUpdateRequest{output: output}, nil
	case CommandUpdate:
		if options.hasInstallation || options.hasRepository || options.hasReference || options.allowDirty || options.hasExpectedDigest || options.hasConflictPolicy {
			return UpdateRequest{installation: options.installation, hasInstallation: options.hasInstallation, source: source, policy: conflictPolicy(options), v1: true, expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, yes: options.yes, output: output}, nil
		}
		return UpdateRequest{expectedCommit: options.expectedCommit, hasExpected: options.hasExpected, yes: options.yes, output: output}, nil
	case CommandPlanSync:
		return PlanSyncRequest{installation: options.installation, selection: selectionOptions(options), allowDirty: options.allowDirty, policy: conflictPolicy(options), output: output}, nil
	case CommandSync:
		return SyncRequest{installation: options.installation, selection: selectionOptions(options), allowDirty: options.allowDirty, expectedDigest: options.expectedDigest, hasExpectedDigest: options.hasExpectedDigest, policy: conflictPolicy(options), yes: options.yes, output: output}, nil
	case CommandDoctor:
		return DoctorRequest{installation: options.installation, testMCP: options.testMCP, yes: options.yes, output: output}, nil
	case CommandPlanRollback:
		return PlanRollbackRequest{installation: options.installation, operation: options.operation, hasOperation: options.hasOperation, policy: conflictPolicy(options), output: output}, nil
	case CommandRollback:
		return RollbackRequest{installation: options.installation, operation: options.operation, hasOperation: options.hasOperation, policy: conflictPolicy(options), yes: options.yes, output: output}, nil
	case CommandHistory:
		return HistoryRequest{installation: options.installation, output: output}, nil
	case CommandPlanHistoryPurge:
		return PlanHistoryPurgeRequest{installation: options.installation, selection: historyPurgeSelection(options), operation: options.operation, output: output}, nil
	case CommandHistoryPurge:
		return HistoryPurgeRequest{installation: options.installation, selection: historyPurgeSelection(options), operation: options.operation, yes: options.yes, output: output}, nil
	case CommandList:
		return ListRequest{target: firstTarget(options.targets), hasTarget: len(options.targets) == 1, scope: options.scope, hasScope: options.hasScope, output: output}, nil
	case CommandStatus:
		return StatusRequest{installation: options.installation, hasInstallation: options.hasInstallation, checkUpdates: options.checkUpdates, output: output}, nil
	case CommandPlanUninstall:
		if options.hasInstallation || options.hasConflictPolicy {
			return PlanUninstallRequest{installation: options.installation, hasInstallation: options.hasInstallation, policy: conflictPolicy(options), v1: true, output: output}, nil
		}
		return PlanUninstallRequest{output: output}, nil
	case CommandUninstall:
		if options.hasInstallation || options.hasConflictPolicy {
			return UninstallRequest{installation: options.installation, hasInstallation: options.hasInstallation, policy: conflictPolicy(options), v1: true, yes: options.yes, output: output}, nil
		}
		return UninstallRequest{yes: options.yes, output: output}, nil
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
	case "plan":
		if len(arguments) < 2 {
			return "", 0, UsageMissingSubcommand
		}
		if strings.HasPrefix(arguments[1], "-") {
			return "", 0, UsageMissingSubcommand
		}
		switch arguments[1] {
		case "install":
			return CommandPlanInstall, 2, ""
		case "update":
			return CommandPlanUpdate, 2, ""
		case "sync":
			return CommandPlanSync, 2, ""
		case "rollback":
			return CommandPlanRollback, 2, ""
		case "uninstall":
			return CommandPlanUninstall, 2, ""
		case "history":
			if len(arguments) >= 3 && arguments[2] == "purge" {
				return CommandPlanHistoryPurge, 3, ""
			}
			return "", 0, UsageUnknownSubcommand
		default:
			return "", 0, UsageUnknownSubcommand
		}
	}
	return "", 0, UsageUnknownCommand
}

func parseOptions(command Command, arguments []string, jsonRequested bool) (parsedOptions, error) {
	var result parsedOptions
	seen := make(map[string]struct{}, len(arguments))
	allowed := commandOptions[command]
	for index := 0; index < len(arguments); index++ {
		token := arguments[index]
		if token == "--" || !strings.HasPrefix(token, "--") || token == "-" {
			return parsedOptions{}, usage(UsageUnexpectedArgument, command, "", jsonRequested, "cli.argument", fault.ReasonInvalidFormat, nil)
		}
		nameValue := strings.SplitN(strings.TrimPrefix(token, "--"), "=", 2)
		name := nameValue[0]
		kind, ok := allowed[name]
		if !ok || name == "" {
			issue := UsageUnknownOption
			safeOption := ""
			if _, known := knownOptions[name]; known {
				issue = UsageInapplicableOption
				safeOption = name
			}
			return parsedOptions{}, usage(issue, command, safeOption, jsonRequested, "cli.option", fault.ReasonUnknownValue, nil)
		}
		repeatable := name == "asset" || name == "bundle" || command == CommandInit && name == "target"
		if _, duplicate := seen[name]; duplicate && !repeatable {
			return parsedOptions{}, usage(UsageDuplicateOption, command, name, jsonRequested, "cli.option", fault.ReasonInvalidFormat, nil)
		}
		seen[name] = struct{}{}
		if kind == booleanOption {
			if len(nameValue) == 2 {
				return parsedOptions{}, usage(UsageUnexpectedOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
			}
			setBoolean(&result, name)
			continue
		}
		var value string
		if len(nameValue) == 2 {
			value = nameValue[1]
		} else {
			if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return parsedOptions{}, usage(UsageMissingOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
			}
			index++
			value = arguments[index]
		}
		if value == "" {
			return parsedOptions{}, usage(UsageEmptyOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
		}
		if err := setValue(&result, name, value); err != nil {
			return parsedOptions{}, usage(UsageInvalidOptionValue, command, name, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, err)
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
	case "check-updates":
		options.checkUpdates = true
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
		return usage(UsageInvalidOptionValue, command, option, jsonRequested, "cli.option_value", fault.ReasonInvalidFormat, nil)
	}
	missing := func(option string) error {
		return usage(UsageMissingOptionValue, command, option, jsonRequested, "cli.option_value", fault.ReasonEmpty, nil)
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
	if options.hasConflictPolicy && options.conflictPolicy == "interactive" && (jsonRequested || isPlanCommand(command)) {
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
		if (options.hasSource || options.allowDirty) && len(options.targets) == 0 {
			return missing("target")
		}
		if options.allowDirty && !options.hasSource {
			return invalid("allow-dirty")
		}
	case CommandBuild:
		if options.allowDirty && !options.hasSource {
			return invalid("allow-dirty")
		}
	case CommandPlanInstall, CommandInstall:
		if options.hasInstallation {
			if options.hasRepository || options.hasReference || options.hasSource || len(options.targets) != 0 || options.hasScope || options.hasProject || hasSelection || options.hasExpected || options.hasExpectedDigest {
				return invalid("installation")
			}
			return nil
		}
		if usesV1InstallOptions(options) || options.hasExpectedDigest {
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
		}
	case CommandPlanUpdate, CommandUpdate:
		usesV1 := options.hasInstallation || options.hasRepository || options.hasReference || options.allowDirty || options.hasExpectedDigest || options.hasConflictPolicy
		if usesV1 && !options.hasInstallation {
			return missing("installation")
		}
	case CommandPlanSync, CommandSync:
		if !options.hasInstallation {
			return missing("installation")
		}
		if !hasSelection {
			return missing("all")
		}
	case CommandDoctor:
		if !options.hasInstallation {
			return missing("installation")
		}
	case CommandPlanRollback, CommandRollback, CommandPlanUninstall, CommandUninstall, CommandHistory:
		if (command == CommandPlanUninstall || command == CommandUninstall) && !options.hasInstallation && !options.hasConflictPolicy {
			return nil
		}
		if !options.hasInstallation {
			return missing("installation")
		}
	case CommandPlanHistoryPurge, CommandHistoryPurge:
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

func usesV1InstallOptions(options parsedOptions) bool {
	return options.hasSource || options.hasInstallation || len(options.targets) != 0 || options.hasScope || options.hasProject || options.all || len(options.assets) != 0 || len(options.bundles) != 0 || options.allowDirty
}

func isPlanCommand(command Command) bool {
	return command == CommandPlanInstall || command == CommandPlanUpdate || command == CommandPlanSync || command == CommandPlanRollback || command == CommandPlanUninstall || command == CommandPlanHistoryPurge
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

func usage(issue UsageIssue, command Command, option string, jsonRequested bool, field string, reason fault.InvalidReason, cause error) *UsageError {
	return newUsageError(issue, command, option, jsonRequested, field, reason, cause)
}
