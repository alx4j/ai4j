package git

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

// RepositoryConfiguration is the closed local configuration written by the
// supported init profile. No remote, include, helper, hook, filter, endpoint,
// or shell-bearing key can be represented.
type RepositoryConfiguration struct {
	audited           bool
	requiredFacts     bool
	ignoreCase        bool
	hasIgnoreCase     bool
	precomposeUnicode bool
	hasPrecompose     bool
	symlinks          bool
	hasSymlinks       bool
}

func (c RepositoryConfiguration) IgnoreCase() (bool, bool) {
	return c.ignoreCase, c.hasIgnoreCase
}
func (c RepositoryConfiguration) PrecomposeUnicode() (bool, bool) {
	return c.precomposeUnicode, c.hasPrecompose
}
func (c RepositoryConfiguration) Symlinks() (bool, bool) { return c.symlinks, c.hasSymlinks }
func (c RepositoryConfiguration) Valid() bool {
	return c.audited && c.requiredFacts && c.hasIgnoreCase == c.ignoreCase &&
		c.hasPrecompose == c.precomposeUnicode && !c.symlinks
}

func AuditLocalConfiguration(data []byte) (RepositoryConfiguration, error) {
	records, err := protocol.ParseConfig(data)
	if err != nil {
		return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureMalformedProtocol)
	}
	required := map[string]string{
		"core.bare":                    "false",
		"core.filemode":                "true",
		"core.logallrefupdates":        "true",
		"core.repositoryformatversion": "0",
	}
	seen := make(map[string]struct{}, len(records))
	configuration := RepositoryConfiguration{audited: true}
	for _, record := range records {
		if _, duplicate := seen[record.Key]; duplicate {
			return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
		}
		seen[record.Key] = struct{}{}
		if expected, ok := required[record.Key]; ok {
			if record.Value != expected {
				return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
			}
			delete(required, record.Key)
			continue
		}
		switch record.Key {
		case "core.ignorecase":
			if record.Value != "true" {
				return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
			}
			configuration.ignoreCase, configuration.hasIgnoreCase = true, true
		case "core.precomposeunicode":
			if record.Value != "true" {
				return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
			}
			configuration.precomposeUnicode, configuration.hasPrecompose = true, true
		case "core.symlinks":
			if record.Value != "false" {
				return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
			}
			configuration.symlinks, configuration.hasSymlinks = false, true
		default:
			return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
		}
	}
	if len(required) != 0 {
		return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
	}
	configuration.requiredFacts = true
	if !configuration.Valid() {
		return RepositoryConfiguration{}, NewExecutorError(OperationAuditConfig, FailureRepositoryConflict)
	}
	return configuration, nil
}

func (RepositoryConfiguration) String() string   { return "<git-local-configuration:redacted>" }
func (RepositoryConfiguration) GoString() string { return "<git-local-configuration:redacted>" }
func (c RepositoryConfiguration) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}
func (c RepositoryConfiguration) MarshalText() ([]byte, error) { return []byte(c.String()), nil }
func (RepositoryConfiguration) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_local_configuration": "redacted"})
}

// ValidateIndex proves that the exact stage-zero index equals the previously
// validated tree inventory. It accepts no unmerged stage or extra path.
func ValidateIndex(inventory TreeInventory, data []byte) error {
	if !inventory.Valid() {
		return NewExecutorError(OperationListIndex, FailureInvalidOperation)
	}
	records, err := protocol.ParseIndex(data)
	if err != nil {
		return NewExecutorError(OperationListIndex, FailureMalformedProtocol)
	}
	entries := inventory.Entries()
	if len(records) != len(entries) {
		return NewExecutorError(OperationListIndex, FailureRepositoryConflict)
	}
	for index, record := range records {
		entry := entries[index]
		if record.Stage != 0 || record.Path != entry.Path().String() || record.Mode != string(entry.Mode()) ||
			record.OID != entry.OID().String() {
			return NewExecutorError(OperationListIndex, FailureRepositoryConflict)
		}
	}
	return nil
}

// ValidateCheckoutAttributes proves that one issued plan-bound batch has the
// closed attribute vector. Successful validation returns a sealed batch proof
// that can contribute to complete checkout coverage.
func ValidateCheckoutAttributes(batch CheckoutAttributeBatch, data []byte) (CheckoutAttributeBatchProof, error) {
	if !batch.Valid() {
		return CheckoutAttributeBatchProof{}, ErrExecutorContract
	}
	validated := batch.Paths()
	records, parseErr := protocol.ParseAttributes(data)
	if parseErr != nil {
		return CheckoutAttributeBatchProof{}, NewExecutorError(OperationCheckAttributes, FailureMalformedProtocol)
	}
	expectedRows := len(validated) * len(closedCheckoutAttributes)
	if len(records) != expectedRows {
		return CheckoutAttributeBatchProof{}, NewExecutorError(OperationCheckAttributes, FailurePolicyRejected)
	}
	row := 0
	for _, path := range validated {
		for _, attribute := range closedCheckoutAttributes {
			record := records[row]
			if record.Path != path.String() || record.Name != attribute || !allowedCheckoutAttributeValue(attribute, record.Value) {
				return CheckoutAttributeBatchProof{}, NewExecutorError(OperationCheckAttributes, FailurePolicyRejected)
			}
			row++
		}
	}
	proof := CheckoutAttributeBatchProof{batch: cloneCheckoutAttributeBatch(batch), seal: issuedCheckoutAttributeProofSeal}
	if !proof.Valid() {
		return CheckoutAttributeBatchProof{}, ErrExecutorContract
	}
	return proof, nil
}

func allowedCheckoutAttributeValue(attribute, value string) bool {
	if value == "unspecified" {
		return true
	}
	return value == "unset" && (attribute == "text" || attribute == "ident")
}

func ValidateCleanStatus(data []byte) error {
	if err := protocol.ParseCleanStatus(data); err != nil {
		return NewExecutorError(OperationStatus, FailureRepositoryConflict)
	}
	return nil
}

func CheckoutAttributeNames() []string {
	return slices.Clone(closedCheckoutAttributes)
}
