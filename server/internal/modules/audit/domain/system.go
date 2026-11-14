package domain

import "strings"

// The name an entry with no actor is shown under, in Vietnamese and in
// English: the system's own work, and an account since removed (Record.Actor).
const (
	SystemActorVI = "Hệ thống"
	SystemActorEN = "System"
)

// NamesSystem reports whether q is that name or a part of it, in either
// language, without regard to case or accents: the way a search matches an
// actor's name. An empty q names nothing.
func NamesSystem(q string) bool {
	needle := Fold(strings.TrimSpace(q))
	return needle != "" && (strings.Contains(Fold(SystemActorVI), needle) || strings.Contains(Fold(SystemActorEN), needle))
}
