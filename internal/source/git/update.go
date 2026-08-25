package git

// UpdateDisposition is the source-specific immutable-ref evaluation result.
// Mapping it to a command result is an application concern in a later story.
type UpdateDisposition string

const (
	UpdateNoChange     UpdateDisposition = "no_change"
	UpdateAvailable    UpdateDisposition = "available"
	UpdatePinned       UpdateDisposition = "pinned"
	UpdateRefRewritten UpdateDisposition = "ref_rewritten"
	UpdateAmbiguous    UpdateDisposition = "ambiguous"
	UpdateDeleted      UpdateDisposition = "deleted"
	UpdateSourceError  UpdateDisposition = "source_error"
)

func (d UpdateDisposition) String() string { return string(d) }
func (d UpdateDisposition) Valid() bool {
	switch d {
	case UpdateNoChange, UpdateAvailable, UpdatePinned, UpdateRefRewritten, UpdateAmbiguous, UpdateDeleted, UpdateSourceError:
		return true
	default:
		return false
	}
}
