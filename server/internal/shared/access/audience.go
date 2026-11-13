package access

// Audience is the web host a session was opened at, as the request's Origin
// names it. A session of AudienceStudent or AudienceStaff belongs to that
// host: its refresh token is kept in that host's cookie and its access token
// is refused by the other host's operations. A session of AudienceAny was
// opened from an origin that is neither host and has no such limit.
type Audience string

// The three audiences.
const (
	AudienceAny     Audience = "any"
	AudienceStudent Audience = "student"
	AudienceStaff   Audience = "staff"
)

// Valid reports whether a is one of the three audiences.
func (a Audience) Valid() bool {
	return a == AudienceAny || a == AudienceStudent || a == AudienceStaff
}

// Admits reports whether an account holding s may hold a session of a. The
// staff host admits an account that Workspaces gives the teacher workspace or
// the Admin console. The student host admits one it gives the student app, and
// one it gives no staff workspace, so every account is admitted at the host
// HomeAudience names. AudienceAny admits every account.
func (a Audience) Admits(s Set) bool {
	staff := opensTeacher(s) || opensAdmin(s)
	switch a {
	case AudienceStaff:
		return staff
	case AudienceStudent:
		return s.Has(LearningTakeTests) || !staff
	default:
		return true
	}
}

// HomeAudience returns the host an account holding s is sent to by a link:
// the staff host when Workspaces gives it the teacher workspace or the Admin
// console, and the student host otherwise.
func HomeAudience(s Set) Audience {
	if opensTeacher(s) || opensAdmin(s) {
		return AudienceStaff
	}
	return AudienceStudent
}

// AdminKeys returns the keys any one of which opens the Admin console, for a
// query that has to class roles the way Workspaces does.
func AdminKeys() []Key { return append([]Key(nil), adminKeys...) }

// Hosts are the origins of the two web hosts, each a scheme and a host with no
// path. The zero value, and two equal origins, describe a deployment with one
// web host, where every session is of AudienceAny.
type Hosts struct {
	Student string
	Staff   string
}

// AudienceOf returns the audience of a request whose Origin header is origin:
// the host it equals, and AudienceAny for any other origin, for none, and
// while the two hosts are one.
func (h Hosts) AudienceOf(origin string) Audience {
	if origin == "" || h.Student == h.Staff {
		return AudienceAny
	}
	switch origin {
	case h.Student:
		return AudienceStudent
	case h.Staff:
		return AudienceStaff
	default:
		return AudienceAny
	}
}

// URL returns the origin of the host a names, and the student host's for
// AudienceAny.
func (h Hosts) URL(a Audience) string {
	if a == AudienceStaff {
		return h.Staff
	}
	return h.Student
}

// Other returns the host an account refused at a signs in at: the staff host
// for AudienceStudent and the student host for AudienceStaff.
func (a Audience) Other() Audience {
	if a == AudienceStudent {
		return AudienceStaff
	}
	return AudienceStudent
}
