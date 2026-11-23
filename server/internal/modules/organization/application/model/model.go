// Package model is what the organization module answers other modules: the sign-in policy identity and classes enforce, the centre's name and contact lines that head every email, and the terms the timetable and the gradebook count by.
package model

import (
	"slices"
	"strings"
	"time"
)

// SignInPolicy is how people sign in, as the centre last saved it.
// AllowedGoogleDomains, lower-case, bounds the Google accounts that may sign
// in, link or join; empty allows every domain.
type SignInPolicy struct {
	Google               bool
	Password             bool
	ForcePasswordChange  bool
	JoinCodes            bool
	AllowedGoogleDomains []string
}

// AllowsGoogleEmail reports whether a Google account with email may be used
// under the domain list. It says nothing about whether Google sign-in is on.
func (p SignInPolicy) AllowsGoogleEmail(email string) bool {
	if len(p.AllowedGoogleDomains) == 0 {
		return true
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return false
	}
	return slices.Contains(p.AllowedGoogleDomains, strings.ToLower(email[at+1:]))
}

// Branding is the centre's name and contact lines. Every field but Name may
// be empty.
type Branding struct {
	Name           string
	ShortName      string
	Website        string
	ContactEmail   string
	FrontDeskPhone string
	FrontDeskHours string
	Address        string
}

// Term is one of the centre's terms (T-R8.3): StartsOn and EndsOn are its
// first and last day, both included, each held as midnight UTC, the form a
// DATE column reads. Terms never overlap, so a day is in one term at most.
type Term struct {
	ID       string
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
}

// Holds reports whether the calendar day of day, read in day's own location,
// is one of the term's days.
func (t Term) Holds(day time.Time) bool {
	y, m, d := day.Date()
	at := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return !at.Before(t.StartsOn) && !at.After(t.EndsOn)
}
