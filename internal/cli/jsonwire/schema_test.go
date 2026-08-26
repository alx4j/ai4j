package jsonwire_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/cli/jsonwire"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/result"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	githubsource "github.com/alx4j/ai4j/internal/source/github"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBase = "https://github.com/alx4j/ai4j/schemas/v1/"

func TestAllCommandResponsesValidateAgainstPublishedSchemas(t *testing.T) {
	t.Parallel()

	schemas := compileSchemas(t)
	for _, fixture := range commandFixtures(t) {
		fixture := fixture
		t.Run(fixture.command.String(), func(t *testing.T) {
			t.Parallel()
			encoded, err := jsonwire.Marshal(fixture.response)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			validateJSON(t, schemas[fixture.command.String()+".json"], encoded)
		})
	}

	usage := usageResponse(t)
	encoded, err := jsonwire.Marshal(usage)
	if err != nil {
		t.Fatalf("Marshal(usage) error = %v", err)
	}
	validateJSON(t, schemas["usage.json"], encoded)
}

func TestListResponseValidatesAgainstPublishedSchema(t *testing.T) {
	t.Parallel()
	id, _ := domain.NewInstallationID("installation_001")
	root, _ := filepath.Abs(".")
	summary, err := cli.NewInstallationSummary(id, "ai4j", cli.BuildTargetClaude, cli.ScopeUser, root, "active", testRecordedSource(t), true, nil, nil, []string{"repository-review"}, "healthy")
	if err != nil {
		t.Fatal(err)
	}
	data, err := cli.NewListData([]cli.InstallationSummary{summary})
	if err != nil {
		t.Fatal(err)
	}
	response, err := cli.NewResponse(cli.CommandList, successResult(t), nil, data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonwire.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	schema := compileSchemas(t)["list.json"]
	validateJSON(t, schema, encoded)
	document := decodeDocument(t, encoded)
	document["data"].(map[string]any)["installations"].([]any)[0].(map[string]any)["health"] = "future"
	if err := schema.Validate(document); err == nil {
		t.Fatal("unknown installation health was accepted")
	}
}

func TestLaterWaveUnsupportedResponsesValidateAgainstPublishedSchemas(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	commands := []cli.Command{cli.CommandSync, cli.CommandDoctor, cli.CommandRollback, cli.CommandHistory, cli.CommandHistoryPurge}
	for _, command := range commands {
		response, err := cli.NewResponse(command, failedResult(t, result.FailureEnvironment), nil, cli.UnavailableData{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := jsonwire.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		validateJSON(t, schemas[command.String()+".json"], encoded)
	}
}

func TestDoctorResponseValidatesAgainstPublishedSchema(t *testing.T) {
	t.Parallel()
	id, _ := domain.NewInstallationID("installation_001")
	check, err := cli.NewDoctorCheck("mcp_registration", cli.DoctorCheckOK, "one MCP declaration is structurally valid")
	if err != nil {
		t.Fatal(err)
	}
	startup, err := cli.NewMCPStartupCheck("claude-tools", "/usr/bin/claude", []string{"mcp", "serve"}, []string{"AI4J_TOKEN"}, "/tmp/project", "package", "timed_out", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := cli.NewDoctorData(id, []cli.DoctorCheck{check}, &startup)
	if err != nil {
		t.Fatal(err)
	}
	response, err := cli.NewResponse(cli.CommandDoctor, successResult(t), nil, data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonwire.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	validateJSON(t, compileSchemas(t)["doctor.json"], encoded)
}

func TestSourceProvenanceGoldenKeepsTypedIdentitiesDistinct(t *testing.T) {
	t.Parallel()

	encoded, err := jsonwire.Marshal(commandFixtures(t)[0].response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var wire jsonwire.Envelope[jsonwire.ValidateData]
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	source, err := json.Marshal(wire.Data.Source)
	if err != nil {
		t.Fatalf("Marshal(source) error = %v", err)
	}
	want := `{"sourceMode":"github","sourceSelection":"built_in_default","repository":"github.com/alx4j/ai4j","requestedRef":null,"resolvedRefKind":"default_branch","resolvedRefName":"main","trackingPolicy":"track_fast_forward","commit":{"objectFormat":"sha1","oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"rootTree":{"objectFormat":"sha1","oid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"checkout":null,"sourceDigest":null,"dirty":false,"renderedDigest":{"algorithm":"sha256","digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},"cliBuildCommit":{"objectFormat":"sha1","oid":"dddddddddddddddddddddddddddddddddddddddd"}}`
	if string(source) != want {
		t.Fatalf("source provenance golden mismatch\ngot:  %s\nwant: %s", source, want)
	}

	explicit := testSourceWithReference(t, "refs/heads/main", gitsource.ResolvedBranch, "main", gitsource.TrackFastForward)
	data, err := cli.NewValidateData(explicit, true, 0, 0, nil)
	if err != nil {
		t.Fatalf("NewValidateData() error = %v", err)
	}
	response, err := cli.NewResponse(cli.CommandValidate, successResult(t), nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	encoded, err = jsonwire.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal(explicit) error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"requestedRef":"refs/heads/main","resolvedRefKind":"branch","resolvedRefName":"main"`)) {
		t.Fatalf("requested and resolved reference fields are not distinct: %s", encoded)
	}
	validateJSON(t, compileSchemas(t)["validate.json"], encoded)
}

func TestLocalDevelopmentSourceResponseValidatesAgainstPublishedSchema(t *testing.T) {
	checkout, _ := filepath.Abs(".")
	digest, _ := domain.NewRenderedDigest(strings.Repeat("e", 64))
	rendered, _ := domain.NewRenderedDigest(strings.Repeat("f", 64))
	build, _ := domain.NewBuildCommit(strings.Repeat("d", 40))
	source, err := cli.NewDevelopmentSource(checkout, digest, rendered, build, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := cli.NewValidateData(source, true, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := cli.NewResponse(cli.CommandValidate, successResult(t), nil, data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonwire.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	validateJSON(t, compileSchemas(t)["validate.json"], encoded)
	document := decodeDocument(t, encoded)
	wireSource := document["data"].(map[string]any)["source"].(map[string]any)
	if wireSource["sourceMode"] != "development_source" || wireSource["dirty"] != true || wireSource["repository"] != nil || wireSource["commit"] != nil {
		t.Fatalf("local wire source = %#v", wireSource)
	}
}

func TestEveryFailureFamilyValidatesWithUnavailableData(t *testing.T) {
	t.Parallel()

	schema := compileSchemas(t)["validate.json"]
	failures := []result.Failure{result.FailureApproval, result.FailureEnvironment, result.FailureSource, result.FailureValidation, result.FailureConflict, result.FailureRecovery, result.FailureInternal}
	for _, failure := range failures {
		failure := failure
		t.Run(failure.String(), func(t *testing.T) {
			t.Parallel()
			response, err := cli.NewResponse(cli.CommandValidate, failedResult(t, failure), nil, cli.UnavailableData{})
			if err != nil {
				t.Fatalf("NewResponse() error = %v", err)
			}
			encoded, marshalErr := jsonwire.Marshal(response)
			if marshalErr != nil {
				t.Fatalf("Marshal() error = %v", marshalErr)
			}
			validateJSON(t, schema, encoded)
		})
	}

	cancelled, err := cli.NewResponse(cli.CommandValidate, cancelledResult(t), nil, cli.UnavailableData{})
	if err != nil {
		t.Fatalf("NewResponse(cancelled) error = %v", err)
	}
	encoded, err := jsonwire.Marshal(cancelled)
	if err != nil {
		t.Fatalf("Marshal(cancelled) error = %v", err)
	}
	validateJSON(t, schema, encoded)
}

func TestMutationRecoverySemanticsValidate(t *testing.T) {
	t.Parallel()

	problem, _ := result.NewProblem("operation_failed", "the operation failed", nil)
	cases := []struct {
		name        string
		facts       result.Facts
		wantExit    int
		wantChanged bool
	}{
		{"committed cleanup with diff", recoveryFacts(problem, result.PhaseCommittedCleanupPending, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureRecovery), 8, true},
		{"committed cleanup without diff", recoveryFacts(problem, result.PhaseCommittedCleanupPending, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithoutDiff, result.FailureRecovery), 8, false},
		{"rolled back cleanup pending", recoveryFacts(problem, result.PhaseRolledBackCleanupPending, result.OutcomeRolledBack, result.MutationStarted, result.DurableChangeNone, result.FailureRecovery), 8, false},
		{"post mutation compensated", recoveryFacts(problem, result.PhaseCompleteRolledBack, result.OutcomeRolledBack, result.MutationStarted, result.DurableChangeNone, result.FailureValidation), 7, false},
		{"pre mutation rolled back", recoveryFacts(problem, result.PhaseCompleteRolledBack, result.OutcomeRolledBack, result.MutationNotStarted, result.DurableChangeNone, result.FailureValidation), 5, false},
	}
	schema := compileSchemas(t)["install.json"]
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			commandResult, err := result.New(tc.facts)
			if err != nil {
				t.Fatalf("result.New() error = %v", err)
			}
			id, _ := domain.NewInstallationID("installation_001")
			final, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
			data, err := cli.NewMutationData(cli.OperationInstall, commandResult, &id, nil, final, result.UpdateNotChecked)
			if err != nil {
				t.Fatalf("NewMutationData() error = %v", err)
			}
			response, err := cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
			if err != nil {
				t.Fatalf("NewResponse() error = %v", err)
			}
			encoded, err := jsonwire.Marshal(response)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			validateJSON(t, schema, encoded)
			var wire jsonwire.Envelope[jsonwire.MutationData]
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if wire.ExitCode != tc.wantExit || wire.Changed != tc.wantChanged {
				t.Fatalf("exit/changed = %d/%t, want %d/%t", wire.ExitCode, wire.Changed, tc.wantExit, tc.wantChanged)
			}
		})
	}
}

func recoveryFacts(problem result.Problem, phase result.Phase, outcome result.Outcome, mutation result.MutationState, durable result.DurableChange, failure result.Failure) result.Facts {
	return result.Facts{Status: result.StatusError, Phase: phase, Outcome: outcome, Mutation: mutation, DurableChange: durable, Failure: failure, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}}
}

func TestSchemaCompatibilityAndClosedEnums(t *testing.T) {
	t.Parallel()

	fixture := commandFixtures(t)[0]
	encoded, err := jsonwire.Marshal(fixture.response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	schema := compileSchemas(t)["validate.json"]

	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	sourceCommit := document["data"].(map[string]any)["source"].(map[string]any)["commit"].(map[string]any)
	if _, duplicated := sourceCommit["repository"]; duplicated {
		t.Fatal("source commit redundantly disclosed a repository")
	}
	source := document["data"].(map[string]any)["source"].(map[string]any)
	for _, field := range []string{"rootTree", "renderedDigest", "cliBuildCommit"} {
		if _, present := source[field]; !present {
			t.Fatalf("source provenance omitted %s", field)
		}
	}
	for _, field := range []string{"rootTree", "cliBuildCommit"} {
		if _, duplicated := source[field].(map[string]any)["repository"]; duplicated {
			t.Fatalf("%s redundantly disclosed a repository", field)
		}
	}
	c1Source := decodeDocument(t, encoded)
	c1Source["data"].(map[string]any)["source"].(map[string]any)["resolvedRefName"] = "main\u009bcontrol"
	if err := schema.Validate(c1Source); err == nil {
		t.Fatal("C1 control in source text was accepted")
	}
	dotGitSource := decodeDocument(t, encoded)
	dotGitSource["data"].(map[string]any)["source"].(map[string]any)["repository"] = "github.com/alx4j/ai4j.git"
	if err := schema.Validate(dotGitSource); err == nil {
		t.Fatal("repository identity ending in .git was accepted")
	}
	for _, field := range []string{"trackingPolicy", "rootTree", "renderedDigest", "cliBuildCommit"} {
		missing := decodeDocument(t, encoded)
		delete(missing["data"].(map[string]any)["source"].(map[string]any), field)
		if err := schema.Validate(missing); err == nil {
			t.Fatalf("source without %s was accepted", field)
		}
	}
	wrongTreeFormat := decodeDocument(t, encoded)
	wrongTreeFormat["data"].(map[string]any)["source"].(map[string]any)["rootTree"].(map[string]any)["objectFormat"] = "sha256"
	if err := schema.Validate(wrongTreeFormat); err == nil {
		t.Fatal("unsupported tree object format was accepted")
	}
	wrongDigestAlgorithm := decodeDocument(t, encoded)
	wrongDigestAlgorithm["data"].(map[string]any)["source"].(map[string]any)["renderedDigest"].(map[string]any)["algorithm"] = "sha1"
	if err := schema.Validate(wrongDigestAlgorithm); err == nil {
		t.Fatal("unsupported rendered digest algorithm was accepted")
	}
	wrongBuildFormat := decodeDocument(t, encoded)
	wrongBuildFormat["data"].(map[string]any)["source"].(map[string]any)["cliBuildCommit"].(map[string]any)["objectFormat"] = "sha256"
	if err := schema.Validate(wrongBuildFormat); err == nil {
		t.Fatal("unsupported CLI build object format was accepted")
	}
	contradictoryTracking := decodeDocument(t, encoded)
	contradictoryTracking["data"].(map[string]any)["source"].(map[string]any)["trackingPolicy"] = "pinned"
	if err := schema.Validate(contradictoryTracking); err == nil {
		t.Fatal("default branch with pinned tracking was accepted")
	}
	document["futureOptionalField"] = "accepted"
	if err := schema.Validate(document); err != nil {
		t.Fatalf("optional additive field rejected: %v", err)
	}

	document["status"] = "future_status"
	if err := schema.Validate(document); err == nil {
		t.Fatal("unknown closed status enum was accepted")
	}

	document = decodeDocument(t, encoded)
	document["changed"] = true
	if err := schema.Validate(document); err == nil {
		t.Fatal("read-only changed=true was accepted")
	}
	document = decodeDocument(t, encoded)
	validation := document["data"].(map[string]any)["validation"].(map[string]any)
	validation["valid"] = false
	validation["errorCount"] = float64(1)
	if err := schema.Validate(document); err == nil {
		t.Fatal("invalid validation summary with success exit was accepted")
	}

	planEncoded, _ := jsonwire.Marshal(commandFixtures(t)[1].response)
	planSchema := compileSchemas(t)["install.json"]
	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"source selection", func(value map[string]any) {
			value["data"].(map[string]any)["source"].(map[string]any)["sourceSelection"] = "future"
		}},
		{"resolved ref kind", func(value map[string]any) {
			value["data"].(map[string]any)["source"].(map[string]any)["resolvedRefKind"] = "future"
		}},
		{"action owner", func(value map[string]any) {
			value["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)["owner"] = "future"
		}},
		{"action kind", func(value map[string]any) {
			value["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)["kind"] = "future"
		}},
		{"action recovery", func(value map[string]any) {
			value["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)["recoveryRequirement"] = "future"
		}},
		{"update disposition", func(value map[string]any) { value["data"].(map[string]any)["updateDisposition"] = "future" }},
	}
	for _, tc := range mutations {
		value := decodeDocument(t, planEncoded)
		tc.mutate(value)
		if err := planSchema.Validate(value); err == nil {
			t.Fatalf("unknown %s enum was accepted", tc.name)
		}
	}
	executable := decodeDocument(t, planEncoded)
	contentItem := executable["data"].(map[string]any)["activeContent"].([]any)[0].(map[string]any)
	contentItem["componentType"] = "mcp"
	contentItem["execution"] = map[string]any{"ownership": "toolkit_owned", "dependency": "required", "command": "server", "args": []any{}, "cwd": nil, "supportedPlaceholders": []any{"${CLAUDE_PLUGIN_ROOT}"}, "environment": []any{"api_key"}}
	if err := planSchema.Validate(executable); err != nil {
		t.Fatalf("valid executable disclosure rejected: %v", err)
	}
	contentItem["execution"].(map[string]any)["ownership"] = "future"
	if err := planSchema.Validate(executable); err == nil {
		t.Fatal("unknown execution ownership enum was accepted")
	}

	conflicted := decodeDocument(t, planEncoded)
	conflicted["data"].(map[string]any)["conflicts"] = []any{map[string]any{"code": "ownership_conflict", "resource": "state", "message": "conflict"}}
	if err := planSchema.Validate(conflicted); err == nil {
		t.Fatal("nonempty conflicts with success exit were accepted")
	}
	pinnedPlan := decodeDocument(t, planEncoded)
	pinnedPlan["data"].(map[string]any)["updateDisposition"] = "pinned"
	if err := planSchema.Validate(pinnedPlan); err == nil {
		t.Fatal("plan pinned disposition with status ok was accepted")
	}
	pinnedPlan["status"] = "no_change"
	pinnedPlan["changed"] = false
	pinnedPlan["exitCode"] = float64(0)
	if err := planSchema.Validate(pinnedPlan); err != nil {
		t.Fatalf("valid pinned plan was rejected: %v", err)
	}
	unknownPlan := decodeDocument(t, planEncoded)
	unknownPlan["data"].(map[string]any)["updateDisposition"] = "unknown"
	if err := planSchema.Validate(unknownPlan); err == nil {
		t.Fatal("plan unknown disposition with success was accepted")
	}
	readOnlyExitSeven := decodeDocument(t, planEncoded)
	readOnlyExitSeven["status"] = "error"
	readOnlyExitSeven["exitCode"] = float64(7)
	readOnlyExitSeven["data"] = nil
	readOnlyExitSeven["errors"] = []any{testErrorDiagnostic()}
	if err := planSchema.Validate(readOnlyExitSeven); err == nil {
		t.Fatal("read-only response with exit 7 was accepted")
	}

	statusEncoded, _ := jsonwire.Marshal(commandFixtures(t)[5].response)
	statusSchema := compileSchemas(t)["status.json"]
	statusDocument := decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["nativeState"].(map[string]any)["registration"] = "future"
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("unknown native enum was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["nativeState"].(map[string]any)["version"].(map[string]any)["observation"] = "future"
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("unknown native version status enum was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["recoveryState"] = map[string]any{"state": "incomplete_journal", "phase": "prepared"}
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("incomplete recovery with success exit was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["recoveryState"].(map[string]any)["state"] = "future"
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("unknown recovery enum was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["status"] = "error"
	statusDocument["exitCode"] = float64(8)
	statusDocument["errors"] = []any{map[string]any{"code": "recovery_required", "message": "recovery required", "context": []any{}}}
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("recovery exit with recovery state none was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["updateDisposition"] = "pinned"
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("status pinned disposition with status ok was accepted")
	}
	statusDocument = decodeDocument(t, statusEncoded)
	statusDocument["data"].(map[string]any)["updateDisposition"] = "unknown"
	if err := statusSchema.Validate(statusDocument); err == nil {
		t.Fatal("status unknown disposition with success was accepted")
	}
	for _, exitCode := range []float64{4, 8} {
		statusDocument = decodeDocument(t, statusEncoded)
		statusDocument["status"] = "error"
		statusDocument["exitCode"] = exitCode
		statusDocument["data"] = nil
		statusDocument["errors"] = []any{testErrorDiagnostic()}
		if err := statusSchema.Validate(statusDocument); err == nil {
			t.Fatalf("status null data with exit %.0f was accepted", exitCode)
		}
	}

	installEncoded, _ := jsonwire.Marshal(commandFixtures(t)[2].response)
	installSchema := compileSchemas(t)["install.json"]
	mutationDocument := decodeDocument(t, installEncoded)
	mutationDocument["changed"] = false
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("committed diff with changed=false was accepted")
	}
	mutationDocument = decodeDocument(t, installEncoded)
	mutationDocument["data"].(map[string]any)["operationResult"].(map[string]any)["phase"] = "applying"
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("applying lifecycle with committed tuple and exit0 was accepted")
	}
	mutationDocument = decodeDocument(t, installEncoded)
	mutationDocument["data"].(map[string]any)["updateDisposition"] = "pinned"
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("mutation pinned disposition with committed success was accepted")
	}
	mutationDocument = decodeDocument(t, installEncoded)
	mutationDocument["data"].(map[string]any)["updateDisposition"] = "unknown"
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("mutation unknown disposition with committed success was accepted")
	}
	mutationDocument = decodeDocument(t, installEncoded)
	mutationDocument["status"] = "error"
	mutationDocument["changed"] = false
	mutationDocument["exitCode"] = float64(7)
	mutationDocument["data"] = nil
	mutationDocument["errors"] = []any{testErrorDiagnostic()}
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("mutation null data with exit 7 was accepted")
	}
	mutationDocument["exitCode"] = float64(3)
	mutationDocument["changed"] = true
	if err := installSchema.Validate(mutationDocument); err == nil {
		t.Fatal("mutation null data with changed=true was accepted")
	}
	mutationDocument["changed"] = false
	if err := installSchema.Validate(mutationDocument); err != nil {
		t.Fatalf("valid mutation null-data failure was rejected: %v", err)
	}

	versionEncoded, _ := jsonwire.Marshal(commandFixtures(t)[8].response)
	versionDocument := decodeDocument(t, versionEncoded)
	versionDocument["data"].(map[string]any)["cliVersion"] = "1.0\u009bcontrol"
	if err := compileSchemas(t)["version.json"].Validate(versionDocument); err == nil {
		t.Fatal("C1 control in CLI version schema field was accepted")
	}
	usageEncoded, _ := jsonwire.Marshal(usageResponse(t))
	usageDocument := decodeDocument(t, usageEncoded)
	usageDocument["errors"].([]any)[0].(map[string]any)["message"] = "usage\u009bcontrol"
	if err := compileSchemas(t)["usage.json"].Validate(usageDocument); err == nil {
		t.Fatal("C1 control in diagnostic schema field was accepted")
	}
}

func TestRoundTripNullEmptyAndDeterminism(t *testing.T) {
	t.Parallel()

	fixtures := commandFixtures(t)
	version := fixtures[len(fixtures)-1].response
	first, err := jsonwire.Marshal(version)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	second, err := jsonwire.Marshal(version)
	if err != nil {
		t.Fatalf("Marshal() second error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("repeated marshal is not byte-identical")
	}

	var decoded jsonwire.Envelope[jsonwire.VersionData]
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("Unmarshal typed DTO error = %v", err)
	}
	roundTrip, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("Marshal typed DTO error = %v", err)
	}
	if !bytes.Equal(first, roundTrip) {
		t.Fatalf("round trip changed bytes\nfirst: %s\nround: %s", first, roundTrip)
	}
	if decoded.OperationID != nil || decoded.Warnings == nil || decoded.Errors == nil || decoded.Data.DefaultSource.Reference != nil {
		t.Fatalf("null/empty semantics lost: %#v", decoded)
	}

	usage, err := jsonwire.Marshal(usageResponse(t))
	if err != nil {
		t.Fatalf("Marshal(usage) error = %v", err)
	}
	want := `{"schemaVersion":1,"command":null,"status":"error","changed":false,"operationId":null,"exitCode":2,"data":null,"warnings":[],"errors":[{"code":"invalid_cli_usage","message":"command line does not match the CLI grammar","context":[{"field":"issue","value":"missing_command"}]}]}`
	if string(usage) != want {
		t.Fatalf("usage golden mismatch\ngot:  %s\nwant: %s", usage, want)
	}
}

func TestExecutableContentMarshalsRequiredEmptyCollectionsAsArrays(t *testing.T) {
	t.Parallel()

	execution, err := cli.NewExecution(cli.ExecutionToolkitOwned, cli.DependencyRequired, "server", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("NewExecution() error = %v", err)
	}
	if execution.Args() == nil || execution.SupportedPlaceholders() == nil || execution.Environment() == nil {
		t.Fatal("execution getters returned nil instead of non-nil empty collections")
	}
	content, err := cli.NewContentItem(cli.ComponentMCP, "example-mcp", "toolkit/mcp/example", strings.Repeat("a", 64), cli.ContentAdded, &execution)
	if err != nil {
		t.Fatalf("NewContentItem() error = %v", err)
	}
	data, err := cli.NewValidateData(testSource(t), true, 0, 0, []cli.ContentItem{content})
	if err != nil {
		t.Fatalf("NewValidateData() error = %v", err)
	}
	response, err := cli.NewResponse(cli.CommandValidate, successResult(t), nil, data)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	encoded, err := jsonwire.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	wantExecution := `"execution":{"ownership":"toolkit_owned","dependency":"required","command":"server","args":[],"cwd":null,"supportedPlaceholders":[],"environment":[]}`
	if !bytes.Contains(encoded, []byte(wantExecution)) {
		t.Fatalf("empty execution collection golden mismatch: %s", encoded)
	}
	validateJSON(t, compileSchemas(t)["validate.json"], encoded)
}

func TestShuffledCollectionsProduceByteIdenticalJSON(t *testing.T) {
	t.Parallel()

	absent, _ := cli.NewCondition("absent", "")
	present, _ := cli.NewCondition("present", "")
	actionA, _ := cli.NewAction(1, "ai4j", "commit_state", "a-resource", absent, present, "none")
	actionB, _ := cli.NewAction(1, "ai4j", "commit_state", "b-resource", absent, present, "none")
	contentA, _ := cli.NewContentItem("skill", "a-skill", "toolkit/a", strings.Repeat("a", 64), cli.ContentAdded, nil)
	contentB, _ := cli.NewContentItem("skill", "b-skill", "toolkit/b", strings.Repeat("b", 64), cli.ContentAdded, nil)
	conflictA, _ := cli.NewConflict("ownership_conflict", "a-resource", "a conflict")
	conflictB, _ := cli.NewConflict("ownership_conflict", "b-resource", "b conflict")
	id, _ := domain.NewInstallationID("installation_001")
	final, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	problem, _ := result.NewProblem("ownership_conflict", "owned state conflicts", nil)
	commandResult, _ := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureConflict, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}})

	build := func(actions []cli.Action, content []cli.ContentItem, conflicts []cli.Conflict) []byte {
		t.Helper()
		data, err := cli.NewPlanData(cli.OperationInstall, testSource(t), id, actions, content, conflicts, final, result.UpdateNotChecked)
		if err != nil {
			t.Fatalf("NewPlanData() error = %v", err)
		}
		response, err := cli.NewResponse(cli.CommandInstall, commandResult, nil, data)
		if err != nil {
			t.Fatalf("NewResponse() error = %v", err)
		}
		encoded, err := jsonwire.Marshal(response)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		return encoded
	}

	forward := build([]cli.Action{actionA, actionB}, []cli.ContentItem{contentA, contentB}, []cli.Conflict{conflictA, conflictB})
	reverse := build([]cli.Action{actionB, actionA}, []cli.ContentItem{contentB, contentA}, []cli.Conflict{conflictB, conflictA})
	if !bytes.Equal(forward, reverse) {
		t.Fatalf("shuffled inputs changed JSON\nforward: %s\nreverse: %s", forward, reverse)
	}
	validateJSON(t, compileSchemas(t)["install.json"], forward)
}

func TestResponseRejectsCommandDataMismatchAndUnsafeUsage(t *testing.T) {
	t.Parallel()

	fixtures := commandFixtures(t)
	plan := fixtures[1].response.Data().(cli.PlanData)
	if _, err := cli.NewResponse(cli.CommandUpdate, fixtures[1].response.Result(), nil, plan); err == nil {
		t.Fatal("command/data mismatch was accepted")
	}
	secret := strings.Repeat("SECRET_CANARY_", 100_000)
	if _, err := cli.NewUsageData(cli.UsageUnknownOption, secret); err == nil {
		t.Fatal("unbounded unknown option was accepted for disclosure")
	}
	if _, err := cli.NewCondition("absent", secret); err == nil {
		t.Fatal("secret checksum on absent condition was accepted")
	}
	problem, _ := result.NewProblem("cleanup_required", "cleanup is required", nil)
	cleanupResult, _ := result.New(recoveryFacts(problem, result.PhaseCommittedCleanupPending, result.OutcomeCommitted, result.MutationStarted, result.DurableCommittedWithDiff, result.FailureRecovery))
	if _, err := cli.NewResponse(cli.CommandInstall, cleanupResult, nil, cli.UnavailableData{}); err == nil {
		t.Fatal("mutation cleanup disclosure was allowed to use null data")
	}
	if _, err := cli.NewResponse(cli.CommandStatus, failedResult(t, result.FailureRecovery), nil, cli.UnavailableData{}); err == nil {
		t.Fatal("status recovery failure was allowed to use null data")
	}
	problem, _ = result.NewProblem("source_failed", "source resolution failed", nil)
	if _, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseApplying, Outcome: result.OutcomePending, Mutation: result.MutationStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureSource, UpdateDisposition: result.UpdateUnknown, Errors: []result.Problem{problem}}); err == nil {
		t.Fatal("result accepted unknown update disposition after mutation")
	}
	if _, err := cli.NewValidateData(testSource(t), false, 0, 0, nil); err == nil {
		t.Fatal("invalid validation with zero errors was accepted")
	}
}

func TestConstructorsRejectMalformedUTFAndOutOfRangeBuildTime(t *testing.T) {
	t.Parallel()

	malformed := string([]byte{0xff, 'x'})
	c1Control := "safe\u009btext"
	if _, err := cli.NewExecution(cli.ExecutionToolkitOwned, cli.DependencyRequired, malformed, nil, "", nil, nil); err == nil {
		t.Fatal("malformed UTF-8 command was accepted")
	}
	if _, err := cli.NewConflict("ownership_conflict", "state", malformed); err == nil {
		t.Fatal("malformed UTF-8 diagnostic text was accepted")
	}
	if _, err := cli.NewExecution(cli.ExecutionToolkitOwned, cli.DependencyRequired, c1Control, nil, "", nil, nil); err == nil {
		t.Fatal("C1 control in execution text was accepted")
	}
	if _, err := cli.NewConflict("ownership_conflict", "state", c1Control); err == nil {
		t.Fatal("C1 control in diagnostic text was accepted")
	}

	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	if _, err := cli.NewSource(gitsource.RenderedProvenance{}); err == nil {
		t.Fatal("source accepted invalid provenance")
	}
	commit, _ := domain.NewBuildCommit(strings.Repeat("b", 40))
	defaultSource, _ := cli.NewDefaultSource(repository, "", cli.DefaultRepositoryBranch)
	id, _ := domain.NewInstallationID("installation_001")
	if _, err := cli.NewInstallation(id, "ai4j", "ai4j_default", testRecordedSource(t), c1Control, "1.0.0", "2.0.0"); err == nil {
		t.Fatal("C1 control in toolkit version was accepted")
	}
	if _, err := cli.NewNativeState(cli.NativeRegistered, cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionRequired, cli.NativePolicyAllowed, c1Control, cli.NativeVersionMatches); err == nil {
		t.Fatal("C1 control in native version was accepted")
	}
	if _, err := cli.NewVersionData("AI4J", "ai4j", c1Control, repository, commit, "go1.26.6", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource); err == nil {
		t.Fatal("C1 control in CLI version was accepted")
	}
	longGoVersion := "go1.26." + strings.Repeat("a", 58)
	if _, err := cli.NewVersionData("AI4J", "ai4j", "1.0.0", repository, commit, longGoVersion, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource); err == nil {
		t.Fatal("goVersion longer than the schema bound was accepted")
	}
	for _, year := range []int{0, 9999} {
		data, err := cli.NewVersionData("AI4J", "ai4j", "1.0.0", repository, commit, "go1.26.6", time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource)
		if err != nil {
			t.Fatalf("valid RFC3339 boundary year %d was rejected: %v", year, err)
		}
		response, err := cli.NewResponse(cli.CommandVersion, successResult(t), nil, data)
		if err != nil {
			t.Fatalf("NewResponse(year %d) error = %v", year, err)
		}
		encoded, err := jsonwire.Marshal(response)
		if err != nil {
			t.Fatalf("Marshal(year %d) error = %v", year, err)
		}
		validateJSON(t, compileSchemas(t)["version.json"], encoded)
	}
	for _, year := range []int{-1, 10000} {
		if _, err := cli.NewVersionData("AI4J", "ai4j", "1.0.0", repository, commit, "go1.26.6", time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource); err == nil {
			t.Fatalf("out-of-range RFC3339 year %d was accepted", year)
		}
	}

	encoded, err := jsonwire.Marshal(commandFixtures(t)[0].response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if bytes.Contains(encoded, []byte("\xef\xbf\xbd")) || bytes.Contains(encoded, []byte(`\ufffd`)) {
		t.Fatalf("JSON contains replacement text: %s", encoded)
	}
}

func TestExecutionRejectsOversizedAndDuplicateRawCollections(t *testing.T) {
	t.Parallel()

	oversizedArgs := make([]string, 129)
	oversizedEnvironment := make([]string, 129)
	for index := range oversizedEnvironment {
		oversizedEnvironment[index] = "DUPLICATE"
	}
	cases := []struct {
		name         string
		args         []string
		placeholders []cli.Placeholder
		environment  []string
	}{
		{"oversized args", oversizedArgs, nil, nil},
		{"oversized duplicate environment", nil, nil, oversizedEnvironment},
		{"oversized duplicate placeholders", nil, []cli.Placeholder{cli.PlaceholderPluginRoot, cli.PlaceholderPluginRoot, cli.PlaceholderPluginRoot}, nil},
		{"duplicate environment", nil, nil, []string{"api_key", "api_key"}},
		{"duplicate placeholder", nil, []cli.Placeholder{cli.PlaceholderPluginRoot, cli.PlaceholderPluginRoot}, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := cli.NewExecution(cli.ExecutionToolkitOwned, cli.DependencyRequired, "server", tc.args, "", tc.placeholders, tc.environment); err == nil {
				t.Fatal("invalid raw collection was accepted")
			}
		})
	}

	valid, err := cli.NewExecution(cli.ExecutionHostResolved, cli.DependencyOptional, "server", []string{"z", "a", "z"}, "", []cli.Placeholder{cli.PlaceholderProjectDir, cli.PlaceholderPluginRoot}, []string{"lower_name", "UPPER_NAME"})
	if err != nil {
		t.Fatalf("NewExecution(valid) error = %v", err)
	}
	if got := valid.Args(); !reflect.DeepEqual(got, []string{"z", "a", "z"}) {
		t.Fatalf("Args() = %#v, want semantic order preserved", got)
	}
	if got := valid.Environment(); !reflect.DeepEqual(got, []string{"UPPER_NAME", "lower_name"}) {
		t.Fatalf("Environment() = %#v, want sorted", got)
	}
}

func TestNativeVersionObservationSemantics(t *testing.T) {
	t.Parallel()

	id, _ := domain.NewInstallationID("installation_001")
	installation, err := cli.NewInstallation(id, "ai4j", "ai4j_default", testRecordedSource(t), "1.0.0", "1.0.0", "2.0.0")
	if err != nil {
		t.Fatalf("NewInstallation() error = %v", err)
	}
	recovery, _ := cli.NewRecoveryState(cli.RecoveryStateNone, "")

	makeNative := func(version string, status cli.NativeVersionStatus) cli.NativeState {
		t.Helper()
		value, newErr := cli.NewNativeState(cli.NativeRegistered, cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionRequired, cli.NativePolicyAllowed, version, status)
		if newErr != nil {
			t.Fatalf("NewNativeState() error = %v", newErr)
		}
		return value
	}
	valid := []cli.NativeState{
		makeNative("2.0.0", cli.NativeVersionMatches),
		makeNative("2.1.0", cli.NativeVersionMismatch),
		makeNative("", cli.NativeVersionUnknown),
		makeNative("", cli.NativeVersionNotObservable),
	}
	for _, native := range valid {
		data, err := cli.NewStatusData(&installation, native, nil, recovery, result.UpdateNotChecked)
		if err != nil {
			t.Fatalf("valid version status %q rejected: %v", native.VersionStatus(), err)
		}
		response, err := cli.NewResponse(cli.CommandStatus, successResult(t), nil, data)
		if err != nil {
			t.Fatalf("NewResponse(%q) error = %v", native.VersionStatus(), err)
		}
		encoded, err := jsonwire.Marshal(response)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", native.VersionStatus(), err)
		}
		validateJSON(t, compileSchemas(t)["status.json"], encoded)
		var wire jsonwire.Envelope[jsonwire.StatusData]
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", native.VersionStatus(), err)
		}
		if wire.Data.NativeState.Version.Expected == nil || *wire.Data.NativeState.Version.Expected != "2.0.0" {
			t.Fatalf("projected expected version for %q = %#v", native.VersionStatus(), wire.Data.NativeState.Version.Expected)
		}
		wantObservation := "observed"
		if native.VersionStatus() == cli.NativeVersionUnknown {
			wantObservation = "unknown"
		} else if native.VersionStatus() == cli.NativeVersionNotObservable {
			wantObservation = "not_observable"
		}
		if wire.Data.NativeState.Version.Observation != wantObservation {
			t.Fatalf("projected observation for %q = %q, want %q", native.VersionStatus(), wire.Data.NativeState.Version.Observation, wantObservation)
		}
		if native.HasVersion() != (wire.Data.NativeState.Version.Observed != nil) {
			t.Fatalf("projected observed-version presence for %q is inconsistent", native.VersionStatus())
		}
	}

	invalid := []cli.NativeState{
		makeNative("2.1.0", cli.NativeVersionMatches),
		makeNative("2.0.0", cli.NativeVersionMismatch),
		makeNative("2.0.0", cli.NativeVersionUnknown),
		makeNative("", cli.NativeVersionNotApplicable),
	}
	for _, native := range invalid {
		if _, err := cli.NewStatusData(&installation, native, nil, recovery, result.UpdateNotChecked); err == nil {
			t.Fatalf("invalid version status %q was accepted", native.VersionStatus())
		}
	}
	if _, err := cli.NewStatusData(nil, makeNative("2.0.0", cli.NativeVersionMatches), nil, recovery, result.UpdateNotChecked); err == nil {
		t.Fatal("version comparison without installation was accepted")
	}

	unobservable, err := cli.NewNativeState(cli.NativeRegistrationNotObservable, cli.NativeInstallationNotObservable, cli.NativeEnablementNotObservable, cli.NativeActivationNotObservable, cli.NativeReloadNotObservable, cli.NativeNextSessionNotObservable, cli.NativePolicyNotObservable, "", cli.NativeVersionNotObservable)
	if err != nil {
		t.Fatalf("not-observable native state rejected: %v", err)
	}
	unobservableData, err := cli.NewStatusData(&installation, unobservable, nil, recovery, result.UpdateNotChecked)
	if err != nil {
		t.Fatalf("not-observable status rejected: %v", err)
	}
	unobservableResponse, err := cli.NewResponse(cli.CommandStatus, successResult(t), nil, unobservableData)
	if err != nil {
		t.Fatalf("NewResponse(not-observable) error = %v", err)
	}
	encoded, err := jsonwire.Marshal(unobservableResponse)
	if err != nil {
		t.Fatalf("Marshal(not-observable) error = %v", err)
	}
	validateJSON(t, compileSchemas(t)["status.json"], encoded)
}

func TestStatusInstallationDispositionSemantics(t *testing.T) {
	t.Parallel()

	nativeAbsent, err := cli.NewNativeState(cli.NativeNotRegistered, cli.NativeNotInstalled, cli.NativeDisabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionNotRequired, cli.NativePolicyAllowed, "", cli.NativeVersionNotApplicable)
	if err != nil {
		t.Fatalf("NewNativeState(absent) error = %v", err)
	}
	recoveryNone, _ := cli.NewRecoveryState(cli.RecoveryStateNone, "")
	absentData, err := cli.NewStatusData(nil, nativeAbsent, nil, recoveryNone, result.UpdateNotInstalled)
	if err != nil {
		t.Fatalf("absent installation with not_installed rejected: %v", err)
	}
	if _, err := cli.NewStatusData(nil, nativeAbsent, nil, recoveryNone, result.UpdateNotChecked); err == nil {
		t.Fatal("ordinary absent installation with not_checked was accepted")
	}
	absentResponse, err := cli.NewResponse(cli.CommandStatus, successResultWithDisposition(t, result.UpdateNotInstalled), nil, absentData)
	if err != nil {
		t.Fatalf("NewResponse(absent) error = %v", err)
	}
	absentEncoded, _ := jsonwire.Marshal(absentResponse)
	statusSchema := compileSchemas(t)["status.json"]
	validateJSON(t, statusSchema, absentEncoded)
	absentDocument := decodeDocument(t, absentEncoded)
	absentDocument["data"].(map[string]any)["updateDisposition"] = "not_checked"
	if err := statusSchema.Validate(absentDocument); err == nil {
		t.Fatal("schema accepted ordinary absent installation with not_checked")
	}

	id, _ := domain.NewInstallationID("installation_001")
	installation, _ := cli.NewInstallation(id, "ai4j", "ai4j_default", testRecordedSource(t), "1.0.0", "1.0.0", "2.0.0")
	nativePresent, _ := cli.NewNativeState(cli.NativeRegistered, cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionRequired, cli.NativePolicyAllowed, "2.0.0", cli.NativeVersionMatches)
	presentData, err := cli.NewStatusData(&installation, nativePresent, nil, recoveryNone, result.UpdateNotChecked)
	if err != nil {
		t.Fatalf("present installation with not_checked rejected: %v", err)
	}
	if _, err := cli.NewStatusData(&installation, nativePresent, nil, recoveryNone, result.UpdateNotInstalled); err == nil {
		t.Fatal("present installation with not_installed was accepted")
	}
	presentResponse, err := cli.NewResponse(cli.CommandStatus, successResult(t), nil, presentData)
	if err != nil {
		t.Fatalf("NewResponse(present) error = %v", err)
	}
	presentEncoded, _ := jsonwire.Marshal(presentResponse)
	validateJSON(t, statusSchema, presentEncoded)
	presentDocument := decodeDocument(t, presentEncoded)
	presentDocument["data"].(map[string]any)["updateDisposition"] = "not_installed"
	if err := statusSchema.Validate(presentDocument); err == nil {
		t.Fatal("schema accepted present installation with not_installed")
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"toolkit version", func(document map[string]any) {
			document["data"].(map[string]any)["installation"].(map[string]any)["toolkitVersion"] = "1.0\u009bcontrol"
		}},
		{"installation CLI version", func(document map[string]any) {
			document["data"].(map[string]any)["installation"].(map[string]any)["cliVersion"] = "1.0\u009bcontrol"
		}},
		{"expected native version", func(document map[string]any) {
			document["data"].(map[string]any)["nativeState"].(map[string]any)["version"].(map[string]any)["expected"] = "2.0\u009bcontrol"
		}},
		{"observed native version", func(document map[string]any) {
			document["data"].(map[string]any)["nativeState"].(map[string]any)["version"].(map[string]any)["observed"] = "2.0\u009bcontrol"
		}},
	} {
		document := decodeDocument(t, presentEncoded)
		tc.mutate(document)
		if err := statusSchema.Validate(document); err == nil {
			t.Fatalf("schema accepted C1 control in %s", tc.name)
		}
	}

	recoveryUnsupported, _ := cli.NewRecoveryState(cli.RecoveryUnsupportedSchema, "")
	recoveryData, err := cli.NewStatusData(nil, nativeAbsent, nil, recoveryUnsupported, result.UpdateNotChecked)
	if err != nil {
		t.Fatalf("unreadable installation with recovery disclosure rejected: %v", err)
	}
	recoveryResponse, err := cli.NewResponse(cli.CommandStatus, failedResult(t, result.FailureRecovery), nil, recoveryData)
	if err != nil {
		t.Fatalf("NewResponse(recovery) error = %v", err)
	}
	recoveryEncoded, _ := jsonwire.Marshal(recoveryResponse)
	validateJSON(t, compileSchemas(t)["status.json"], recoveryEncoded)
}

func TestWireDTOsContainNoMapInterfaceOrRawJSONFields(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{
		reflect.TypeOf(jsonwire.Envelope[jsonwire.ValidateData]{}), reflect.TypeOf(jsonwire.Diagnostic{}),
		reflect.TypeOf(jsonwire.ValidateData{}), reflect.TypeOf(jsonwire.PlanData{}), reflect.TypeOf(jsonwire.MutationData{}),
		reflect.TypeOf(jsonwire.StatusData{}), reflect.TypeOf(jsonwire.DoctorData{}), reflect.TypeOf(jsonwire.VersionData{}),
	}
	seen := map[reflect.Type]bool{}
	var inspect func(reflect.Type)
	inspect = func(value reflect.Type) {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			value = value.Elem()
		}
		if seen[value] {
			return
		}
		seen[value] = true
		if value.Kind() == reflect.Map || value.Kind() == reflect.Interface {
			t.Fatalf("wire DTO contains forbidden %s type %s", value.Kind(), value)
		}
		if value == reflect.TypeOf(json.RawMessage{}) {
			t.Fatalf("wire DTO contains json.RawMessage")
		}
		if value.Kind() != reflect.Struct {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			inspect(value.Field(index).Type)
		}
	}
	for _, value := range types {
		inspect(value)
	}
}

func TestClosedNeutralEnumsRejectUnknownValues(t *testing.T) {
	t.Parallel()

	absent, _ := cli.NewCondition(cli.ConditionAbsent, "")
	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	cases := []struct {
		name string
		make func() error
	}{
		{"execution ownership", func() error {
			_, err := cli.NewExecution(cli.ExecutionOwnership("future"), cli.DependencyRequired, "tool", nil, "", nil, nil)
			return err
		}},
		{"execution dependency", func() error {
			_, err := cli.NewExecution(cli.ExecutionToolkitOwned, cli.ExecutionDependency("future"), "tool", nil, "", nil, nil)
			return err
		}},
		{"action owner", func() error {
			_, err := cli.NewAction(1, cli.ActionOwner("future"), cli.ActionCommitState, "state", absent, absent, cli.RecoveryNone)
			return err
		}},
		{"action kind", func() error {
			_, err := cli.NewAction(1, cli.ActionOwnerAI4J, cli.ActionKind("future"), "state", absent, absent, cli.RecoveryNone)
			return err
		}},
		{"condition", func() error { _, err := cli.NewCondition(cli.ConditionState("future"), ""); return err }},
		{"native", func() error {
			_, err := cli.NewNativeState(cli.NativeRegistration("future"), cli.NativeInstalled, cli.NativeEnabled, cli.NativeInactive, cli.NativeReloadNotRequired, cli.NativeNextSessionNotRequired, cli.NativePolicyAllowed, "", cli.NativeVersionNotApplicable)
			return err
		}},
		{"drift", func() error { _, err := cli.NewDrift("state", cli.DriftState("future")); return err }},
		{"recovery", func() error { _, err := cli.NewRecoveryState(cli.RecoveryKind("future"), ""); return err }},
		{"default policy", func() error {
			_, err := cli.NewDefaultSource(repository, "", cli.DefaultRefPolicy("future"))
			return err
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.make(); err == nil {
				t.Fatal("unknown enum was accepted")
			}
		})
	}
}

type fixture struct {
	command  cli.Command
	response cli.Response
}

func commandFixtures(t *testing.T) []fixture {
	t.Helper()
	source := testSource(t)
	id, _ := domain.NewInstallationID("installation_001")
	finalPresent, _ := cli.NewFinalState(cli.StatePresent, cli.StatePresent, cli.StatePresent)
	finalAbsent, _ := cli.NewFinalState(cli.StateAbsent, cli.StateAbsent, cli.StateAbsent)
	absent, _ := cli.NewCondition("absent", "")
	present, _ := cli.NewCondition("present", "")
	action, _ := cli.NewAction(1, "ai4j", "commit_state", "installation", absent, present, "none")
	content, _ := cli.NewContentItem("skill", "review", "toolkit/skills/review", strings.Repeat("a", 64), cli.ContentAdded, nil)

	readResult := successResult(t)
	validateData, _ := cli.NewValidateData(source, true, 0, 0, []cli.ContentItem{content})
	planInstall, _ := cli.NewPlanData(cli.OperationInstall, source, id, []cli.Action{action}, []cli.ContentItem{content}, nil, finalPresent, result.UpdateNotChecked)
	planUpdate, _ := cli.NewPlanData(cli.OperationUpdate, source, id, []cli.Action{action}, []cli.ContentItem{content}, nil, finalPresent, result.UpdateNotChecked)
	planUninstall, _ := cli.NewPlanData(cli.OperationUninstall, source, id, []cli.Action{action}, []cli.ContentItem{content}, nil, finalAbsent, result.UpdateNotChecked)

	committed := committedResult(t)
	installData, _ := cli.NewMutationData(cli.OperationInstall, committed, &id, []cli.Action{action}, finalPresent, result.UpdateNotChecked)
	updateData, _ := cli.NewMutationData(cli.OperationUpdate, committed, &id, []cli.Action{action}, finalPresent, result.UpdateNotChecked)
	uninstallData, _ := cli.NewMutationData(cli.OperationUninstall, committed, &id, []cli.Action{action}, finalAbsent, result.UpdateNotChecked)

	native, _ := cli.NewNativeState("registered", "installed", "enabled", "inactive", "not_required", "required", "allowed", "", cli.NativeVersionNotApplicable)
	recovery, _ := cli.NewRecoveryState("none", "")
	statusData, _ := cli.NewStatusData(nil, native, nil, recovery, result.UpdateNotInstalled)
	statusResult := successResultWithDisposition(t, result.UpdateNotInstalled)

	repository, _ := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	buildCommit, _ := domain.NewBuildCommit(strings.Repeat("b", 40))
	defaultSource, _ := cli.NewDefaultSource(repository, "", "repository_default_branch")
	versionData, _ := cli.NewVersionData("AI4J", "ai4j", "0.0.0-dev", repository, buildCommit, "go1.26.6", time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC), "darwin", "arm64", defaultSource)

	values := []struct {
		command       cli.Command
		commandResult result.Result
		data          cli.Data
	}{
		{cli.CommandValidate, readResult, validateData}, {cli.CommandInstall, readResult, planInstall},
		{cli.CommandInstall, committed, installData}, {cli.CommandUpdate, readResult, planUpdate},
		{cli.CommandUpdate, committed, updateData}, {cli.CommandStatus, statusResult, statusData},
		{cli.CommandUninstall, readResult, planUninstall}, {cli.CommandUninstall, committed, uninstallData},
		{cli.CommandVersion, readResult, versionData},
	}
	fixtures := make([]fixture, len(values))
	for index, value := range values {
		response, err := cli.NewResponse(value.command, value.commandResult, nil, value.data)
		if err != nil {
			t.Fatalf("NewResponse(%s) error = %v", value.command, err)
		}
		fixtures[index] = fixture{command: value.command, response: response}
	}
	return fixtures
}

func testSource(t *testing.T) cli.Source {
	t.Helper()
	return testSourceWithReference(t, "", gitsource.ResolvedDefaultBranch, "main", gitsource.TrackFastForward)
}

func testRecordedSource(t *testing.T) cli.RecordedSource {
	t.Helper()
	source := testSourceWithReference(t, "main", gitsource.ResolvedBranch, "main", gitsource.TrackFastForward)
	value, err := cli.NewRecordedSource(
		source.Selection(),
		source.Repository(),
		source.RequestedRef(),
		source.HasRequestedRef(),
		source.ResolvedRefKind(),
		source.Commit().OID(),
	)
	if err != nil {
		t.Fatalf("NewRecordedSource() error = %v", err)
	}
	return value
}

func testSourceWithReference(t *testing.T, reference string, kind gitsource.ResolvedReferenceKind, resolvedName string, tracking gitsource.TrackingPolicy) cli.Source {
	t.Helper()
	selection, err := githubsource.NewSelectionInput("", false, reference, reference != "")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := githubsource.Resolve(selection)
	if err != nil {
		t.Fatal(err)
	}
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	commitOID := strings.Repeat("a", 40)
	advertisementData := commitOID + "\trefs/heads/" + resolvedName + "\n"
	if kind == gitsource.ResolvedDefaultBranch {
		advertisementData = "ref: refs/heads/" + resolvedName + "\tHEAD\n" + commitOID + "\tHEAD\n" + advertisementData
	} else if kind == gitsource.ResolvedTag {
		advertisementData = commitOID + "\trefs/tags/" + resolvedName + "\n"
	} else if kind == gitsource.ResolvedCommit {
		advertisementData = ""
	}
	advertisement, err := gitsource.ParseRemoteAdvertisement(request, []byte(advertisementData))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := gitsource.ResolveReference(request, advertisement)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Resolved().Kind() != kind || resolution.Resolved().Name() != resolvedName {
		t.Fatalf("resolved reference = %#v", resolution.Resolved())
	}
	selected, err := gitsource.NewSelectedObjectProof(resolution, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitsource.NewDirectProvenCommit(selected)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := gitsource.NewCommitTreeProof(commit, []byte(strings.Repeat("b", 40)+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		t.Fatal(err)
	}
	if provenance.TrackingPolicy() != tracking {
		t.Fatalf("tracking policy = %q, want %q", provenance.TrackingPolicy(), tracking)
	}
	digest, _ := domain.NewRenderedDigest(strings.Repeat("c", 64))
	build, _ := domain.NewBuildCommit(strings.Repeat("d", 40))
	rendered, err := gitsource.NewRenderedProvenance(provenance, digest, build)
	if err != nil {
		t.Fatal(err)
	}
	source, err := cli.NewSource(rendered)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	return source
}

func successResult(t *testing.T) result.Result {
	t.Helper()
	return successResultWithDisposition(t, result.UpdateNotChecked)
}

func successResultWithDisposition(t *testing.T, disposition result.UpdateDisposition) result.Result {
	t.Helper()
	value, err := result.New(result.Facts{Status: result.StatusOK, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureNone, UpdateDisposition: disposition})
	if err != nil {
		t.Fatalf("result.New(success) error = %v", err)
	}
	return value
}

func committedResult(t *testing.T) result.Result {
	t.Helper()
	value, err := result.New(result.Facts{Status: result.StatusOK, Phase: result.PhaseComplete, Outcome: result.OutcomeCommitted, Mutation: result.MutationStarted, DurableChange: result.DurableCommittedWithDiff, Failure: result.FailureNone, UpdateDisposition: result.UpdateNotChecked})
	if err != nil {
		t.Fatalf("result.New(committed) error = %v", err)
	}
	return value
}

func failedResult(t *testing.T, failure result.Failure) result.Result {
	t.Helper()
	problem, _ := result.NewProblem("operation_failed", "the operation failed", nil)
	value, err := result.New(result.Facts{Status: result.StatusError, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: failure, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}})
	if err != nil {
		t.Fatalf("result.New(%s) error = %v", failure, err)
	}
	return value
}

func cancelledResult(t *testing.T) result.Result {
	t.Helper()
	problem, _ := result.NewProblem("cancelled", "the operation was cancelled", nil)
	value, err := result.New(result.Facts{Status: result.StatusCancelled, Phase: result.PhaseNone, Outcome: result.OutcomeNone, Mutation: result.MutationNotStarted, DurableChange: result.DurableChangeNone, Failure: result.FailureCancellation, UpdateDisposition: result.UpdateNotChecked, Errors: []result.Problem{problem}})
	if err != nil {
		t.Fatalf("result.New(cancelled) error = %v", err)
	}
	return value
}

func usageResponse(t *testing.T) cli.Response {
	t.Helper()
	data, _ := cli.NewUsageData(cli.UsageMissingCommand, "")
	response, err := cli.NewResponse("", failedResult(t, result.FailureUsage), nil, data)
	if err != nil {
		t.Fatalf("NewResponse(usage) error = %v", err)
	}
	return response
}

func compileSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	directory := filepath.Join("..", "..", "..", "schemas", "v1")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), readErr)
		}
		var document any
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.UseNumber()
		if decodeErr := decoder.Decode(&document); decodeErr != nil {
			t.Fatalf("decode schema %s: %v", entry.Name(), decodeErr)
		}
		if addErr := compiler.AddResource(schemaBase+entry.Name(), document); addErr != nil {
			t.Fatalf("AddResource(%s) error = %v", entry.Name(), addErr)
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, name := range []string{"common.json", "usage.json", "init.json", "validate.json", "build.json", "install.json", "update.json", "sync.json", "list.json", "status.json", "doctor.json", "rollback.json", "uninstall.json", "history.json", "history.purge.json", "version.json"} {
		schema, compileErr := compiler.Compile(schemaBase + name)
		if compileErr != nil {
			t.Fatalf("Compile(%s) error = %v", name, compileErr)
		}
		compiled[name] = schema
	}
	return compiled
}

func validateJSON(t *testing.T, schema *jsonschema.Schema, encoded []byte) {
	t.Helper()
	var document any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode response %s: %v", encoded, err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("schema validation failed for %s: %v", encoded, err)
	}
}

func decodeDocument(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	return document
}

func testErrorDiagnostic() map[string]any {
	return map[string]any{"code": "operation_failed", "message": "the operation failed", "context": []any{}}
}
