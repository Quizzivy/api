package domain

// Ownership is a live test's title and owner, as a reader that may read it
// sees them: what another module needs to name the test and decide who owns
// it (the sharing module's grants). Access is what that reader may do with it.
type Ownership struct {
	TestID  string
	Title   string
	OwnerID string
	Access  Access
}
