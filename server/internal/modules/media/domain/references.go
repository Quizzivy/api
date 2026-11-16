package domain

// TestRef names one published version that uses an asset.
type TestRef struct {
	ID      string
	Title   string
	Version int
}

// VersionUses is what of the published versions uses one asset: Tests names
// those of the tests the asset's own owner owns, by title then version, and
// Count counts every owner's versions, those included. Another owner's
// versions, a copy's (T-R7.7), are only counted, so no title of another
// teacher's test leaves the server through the owner's library.
type VersionUses struct {
	Tests []TestRef
	Count int
}

// ReferencedError is ErrReferenced carrying the versions and independent groups that prevent deletion.
type ReferencedError struct {
	Tests  []TestRef
	Groups []GroupRef
}

// GroupRef names an independent group that owns a material or member reference to an asset.
type GroupRef struct {
	ID     string
	Title  string
	TestID *string
}

func (e *ReferencedError) Error() string { return ErrReferenced.Error() }

func (e *ReferencedError) Is(target error) bool { return target == ErrReferenced }

// InUseElsewhereError is ErrInUseElsewhere carrying how many pieces of other
// owners' content use the asset, which are never named.
type InUseElsewhereError struct {
	Count int
}

func (e *InUseElsewhereError) Error() string { return ErrInUseElsewhere.Error() }

func (e *InUseElsewhereError) Is(target error) bool { return target == ErrInUseElsewhere }
