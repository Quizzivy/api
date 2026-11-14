package domain

import "strings"

// The name an entry with no actor is shown under, in Vietnamese and in
// English: the system's own work, and an account since removed (Record.Actor).
const (
	SystemActorVI = "Hệ thống"
	SystemActorEN = "System"
)

// NamesSystem reports whether q is that name or the beginning of it, in either
// language, without regard to case or accents. An empty q names nothing.
//
// It is the beginning and not any part, as an actor's name is matched by,
// because this one name is weighed against every search: by any part, a search
// for a person called Hồng or Thông ("hong" and "thong" are in "he thong")
// would read every entry nobody is recorded for.
func NamesSystem(q string) bool {
	needle := Fold(strings.TrimSpace(q))
	return needle != "" && (strings.HasPrefix(Fold(SystemActorVI), needle) || strings.HasPrefix(Fold(SystemActorEN), needle))
}
