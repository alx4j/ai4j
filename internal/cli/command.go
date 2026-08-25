// Package cli owns the stable AI4J command grammar and response pairing.
package cli

type Command string

const (
	CommandInit             Command = "init"
	CommandValidate         Command = "validate"
	CommandBuild            Command = "build"
	CommandPlanInstall      Command = "plan.install"
	CommandInstall          Command = "install"
	CommandPlanUpdate       Command = "plan.update"
	CommandUpdate           Command = "update"
	CommandPlanSync         Command = "plan.sync"
	CommandSync             Command = "sync"
	CommandList             Command = "list"
	CommandStatus           Command = "status"
	CommandDoctor           Command = "doctor"
	CommandPlanRollback     Command = "plan.rollback"
	CommandRollback         Command = "rollback"
	CommandPlanUninstall    Command = "plan.uninstall"
	CommandUninstall        Command = "uninstall"
	CommandHistory          Command = "history"
	CommandPlanHistoryPurge Command = "plan.history.purge"
	CommandHistoryPurge     Command = "history.purge"
	CommandVersion          Command = "version"
)

func (c Command) String() string { return string(c) }

func (c Command) Valid() bool {
	switch c {
	case CommandInit, CommandValidate, CommandBuild, CommandPlanInstall, CommandInstall, CommandPlanUpdate, CommandUpdate,
		CommandPlanSync, CommandSync, CommandList, CommandStatus, CommandDoctor, CommandPlanRollback, CommandRollback,
		CommandPlanUninstall, CommandUninstall, CommandHistory, CommandPlanHistoryPurge, CommandHistoryPurge, CommandVersion:
		return true
	default:
		return false
	}
}

func Commands() []Command {
	return []Command{
		CommandInit, CommandValidate, CommandBuild, CommandPlanInstall, CommandInstall, CommandPlanUpdate, CommandUpdate,
		CommandPlanSync, CommandSync, CommandList, CommandStatus, CommandDoctor, CommandPlanRollback, CommandRollback,
		CommandPlanUninstall, CommandUninstall, CommandHistory, CommandPlanHistoryPurge, CommandHistoryPurge, CommandVersion,
	}
}

type OutputMode string

const (
	OutputHuman OutputMode = "human"
	OutputJSON  OutputMode = "json"
)
