// Package domain is the consultation requests' model: what the landing site's form sends and how it is made fit to store, the two requests that are answered and not stored, a stored request and how the centre handled it, and the name a notification may carry. A request holds a name and a phone number given with consent, and nothing about the caller.
package domain

import (
	"strings"
	"time"

	"quizzivy/internal/shared/validation"
)

// Status is how far the centre has got with a request.
type Status string

// The statuses. A request is New until someone at the centre changes it.
const (
	StatusNew       Status = "new"
	StatusContacted Status = "contacted"
	StatusClosed    Status = "closed"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool {
	return s == StatusNew || s == StatusContacted || s == StatusClosed
}

// The bounds a request keeps, as the database checks them, and the form's cap
// on repeats: a number with RepeatCap requests stored in the last RepeatWindow
// gets no further one.
const (
	MaxFullName    = 120
	MinPhoneDigits = 9
	MaxPhoneDigits = 15
	MaxNote        = 1000
	RepeatCap      = 2
	RepeatWindow   = 24 * time.Hour
)

// The languages a request is sent in.
const (
	LocaleVI = "vi"
	LocaleEN = "en"
)

// The fields a submission is refused by, as the contract names them.
const (
	FieldFullName = "fullName"
	FieldPhone    = "phone"
	FieldNote     = "note"
)

// Form is what the consultation form sent, as typed. Website is the field no
// person sees or fills.
type Form struct {
	FullName             string
	Phone                string
	Interests            []string
	CourseFor            string
	CallTime             string
	Locale               string
	PrivacyNoticeVersion string
	Website              string
}

// Submission is a Form made fit to store. Honeypot says the form's unseen
// field was filled.
type Submission struct {
	FullName             string
	PhoneDigits          string
	Interests            []string
	CourseFor            string
	CallTime             string
	Locale               string
	PrivacyNoticeVersion string
	Honeypot             bool
}

// Normalised returns the form as it would be stored: the name through
// validation.CleanName and the phone number as its digits. It refuses, with a
// *validation.Error naming fullName, phone or both, a name that is empty, too
// long, without a visible character or holding a hidden one, and a number of
// fewer than MinPhoneDigits or more than MaxPhoneDigits digits. No message
// repeats a value.
func (f Form) Normalised() (Submission, error) {
	var refused validation.Error
	name, fault, _ := validation.CleanName(f.FullName, MaxFullName)
	switch fault {
	case validation.NameOK:
	case validation.NameEmpty:
		refused.AddIn(FieldFullName, "Họ và tên không được để trống.", "The full name cannot be empty.")
	case validation.NameTooLong:
		refused.AddIn(FieldFullName, "Họ và tên không được dài quá 120 ký tự.", "The full name cannot be longer than 120 characters.")
	case validation.NameHidden:
		refused.AddIn(FieldFullName, "Họ và tên không được chứa ký tự điều khiển hoặc ký tự vô hình.", "The full name cannot contain a control character or an invisible character.")
	default:
		refused.AddIn(FieldFullName, "Họ và tên phải có ít nhất một chữ cái, chữ số, dấu câu hoặc ký hiệu.", "The full name must have at least one letter, digit, punctuation mark or symbol.")
	}
	digits := Digits(f.Phone)
	if len(digits) < MinPhoneDigits || len(digits) > MaxPhoneDigits {
		refused.AddIn(FieldPhone, "Số điện thoại phải có từ 9 đến 15 chữ số.", "The phone number must have 9 to 15 digits.")
	}
	if err := refused.OrNil(); err != nil {
		return Submission{}, err
	}
	interests := f.Interests
	if interests == nil {
		interests = []string{}
	}
	return Submission{
		FullName: name, PhoneDigits: digits, Interests: interests, CourseFor: f.CourseFor, CallTime: f.CallTime,
		Locale: f.Locale, PrivacyNoticeVersion: f.PrivacyNoticeVersion, Honeypot: f.Website != "",
	}, nil
}

// Digits returns the digits 0 to 9 of s, in order, and nothing else of it.
func Digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// Drop is why a submission that passed validation was not stored.
type Drop string

// The reasons. Stored is the empty reason of a submission that was stored.
const (
	Stored       Drop = ""
	DropHoneypot Drop = "honeypot"
	DropRepeat   Drop = "repeat"
)

// Verdict says whether s is stored, given recent, the requests its number
// already has stored from the last RepeatWindow. The honeypot is judged first.
func (s Submission) Verdict(recent int) Drop {
	switch {
	case s.Honeypot:
		return DropHoneypot
	case recent >= RepeatCap:
		return DropRepeat
	}
	return Stored
}

// GivenName is the one word of a normalised full name a notification may
// carry: the last for a request sent in Vietnamese, where the given name
// closes the name, and the first otherwise.
func GivenName(fullName, locale string) string {
	words := strings.Fields(fullName)
	switch {
	case len(words) == 0:
		return ""
	case locale == LocaleVI:
		return words[len(words)-1]
	}
	return words[0]
}

// Handler is the account that last changed a request's status.
type Handler struct {
	ID   string
	Name string
}

// Lead is a stored consultation request. Note is empty when the centre wrote
// none; HandledBy is nil while nobody has changed the status and once that
// account is gone, and HandledAt is nil until the status first changes.
type Lead struct {
	ID                   string
	FullName             string
	PhoneDigits          string
	Interests            []string
	CourseFor            string
	CallTime             string
	Locale               string
	PrivacyNoticeVersion string
	ConsentedAt          time.Time
	Status               Status
	Note                 string
	HandledBy            *Handler
	HandledAt            *time.Time
	CreatedAt            time.Time
}
