package git

import (
	"slices"

	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

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

// ValidateCheckoutAttributes proves that one planned batch has the closed
// attribute vector. Successful validation returns an opaque proof that can
// contribute to complete checkout coverage.
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
	proof := CheckoutAttributeBatchProof{batch: cloneCheckoutAttributeBatch(batch)}
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
