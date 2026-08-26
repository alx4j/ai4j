package jsonwire_test

import (
	"bytes"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonwire"
	"github.com/alx4j/ai4j/internal/domain"
)

func TestOfflineLifecyclePlansDiscloseNullSourceAndValidate(t *testing.T) {
	t.Parallel()
	id, _ := domain.NewInstallationID("installation_001")
	finalPresent, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	finalAbsent, _ := cli.NewFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	absent, _ := cli.NewCondition(cli.ConditionAbsent, "")
	present, _ := cli.NewCondition(cli.ConditionPresent, "")
	action, _ := cli.NewAction(1, cli.ActionOwnerAI4J, cli.ActionCommitState, "installation", absent, present, cli.RecoveryNone)

	for _, test := range []struct {
		command   cli.Command
		operation cli.Operation
		final     cli.FinalState
	}{
		{command: cli.CommandRollback, operation: cli.OperationRollback, final: finalPresent},
		{command: cli.CommandUninstall, operation: cli.OperationUninstall, final: finalAbsent},
		{command: cli.CommandHistoryPurge, operation: cli.OperationHistoryPurge, final: finalPresent},
	} {
		t.Run(test.operation.String(), func(t *testing.T) {
			data, err := cli.NewOfflinePlanData(test.operation, id, []cli.Action{action}, nil, test.final)
			if err != nil {
				t.Fatal(err)
			}
			response, err := cli.NewResponse(test.command, successResult(t), nil, data)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := jsonwire.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(encoded, []byte(`"source":null`)) {
				t.Fatalf("offline plan omitted null source: %s", encoded)
			}
			validateJSON(t, compileSchemas(t)[test.command.String()+".json"], encoded)
		})
	}
}
