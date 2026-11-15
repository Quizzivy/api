package domain

import "errors"

// ErrNotFound means no request has the id.
var ErrNotFound = errors.New("leads: no such request")
