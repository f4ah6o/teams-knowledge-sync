package domain

import "time"

type Calendar struct {
	ID, Name, Owner, Color                      string
	IsDefault, CanEdit, CanViewPrivate, Enabled bool
	RawJSON                                     []byte
}
type CalendarEvent struct {
	ID, CalendarID, ICalUID, SeriesMasterID, EventType                  string
	Subject, BodyHTML, BodyText, BodyPreview                            string
	StartTimezone, EndTimezone, OriginalStart                           string
	OrganizerAddress, OrganizerName                                     string
	LocationName, OnlineMeetingURL, JoinURL, WebURL                     string
	ShowAs, Importance, Sensitivity, ResponseStatus, ETag               string
	StartUTC, EndUTC                                                    time.Time
	IsAllDay, IsCancelled, IsOrganizer, IsOnlineMeeting, HasAttachments bool
	CreatedAt, ModifiedAt, DeletedAt                                    *time.Time
	RawJSON                                                             []byte
	Attendees                                                           []Attendee
	Locations                                                           []string
	Categories                                                          []string
	Attachments                                                         []MailAttachment
}
type Attendee struct {
	Type, Address, Name, Response string
}
type EventSearchFilter struct {
	Query, CalendarID string
	From, To          *time.Time
	Limit             int
}
