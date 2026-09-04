// Package cli owns the stable AI4J command grammar and response pairing.
package cli

type Command string

const (
	CommandInit         Command = "init"
	CommandValidate     Command = "validate"
	CommandBuild        Command = "build"
	CommandInstall      Command = "install"
	CommandUpdate       Command = "update"
	CommandSync         Command = "sync"
	CommandList         Command = "list"
	CommandStatus       Command = "status"
	CommandDoctor       Command = "doctor"
	CommandRollback     Command = "rollback"
	CommandUninstall    Command = "uninstall"
	CommandHistory      Command = "history"
	CommandHistoryPurge Command = "history.purge"
	CommandVersion      Command = "version"
)

func (c Command) String() string { return string(c) }

func (c Command) Valid() bool {
	switch c {
	case CommandInit, CommandValidate, CommandBuild, CommandInstall, CommandUpdate, CommandSync, CommandList, CommandStatus,
		CommandDoctor, CommandRollback, CommandUninstall, CommandHistory, CommandHistoryPurge, CommandVersion:
		return true
	default:
		return false
	}
}

// UsesPositionalInstallation reports whether the command grammar places an
// installation ID immediately after the command name.
func (c Command) UsesPositionalInstallation() bool {
	switch c {
	case CommandUpdate, CommandSync, CommandStatus, CommandDoctor, CommandRollback, CommandUninstall, CommandHistory, CommandHistoryPurge:
		return true
	default:
		return false
	}
}

type OutputMode string

const (
	OutputHuman OutputMode = "human"
	OutputJSON  OutputMode = "json"
)
