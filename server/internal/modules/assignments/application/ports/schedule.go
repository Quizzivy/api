package ports

// ScheduleNudge tells the notification scheduler in this process that an
// assignment's window or a student's close may have moved, so that it reads
// its next moment again at once (T-R7.14).
type ScheduleNudge interface {
	Nudge()
}
