package calendar

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
)

// PrivatePolicy controls whether details of sensitivity=private events are
// stored or masked.
type PrivatePolicy struct {
	StoreDetails bool
}

const maskedSubject = "(非公開)"

var teamsJoinURL = regexp.MustCompile(`https://teams\.microsoft\.com/l/meetup-join/[^\s"'<>]+`)

type dateTimeTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// parseGraphTime parses a Graph dateTimeTimeZone returned with
// Prefer: outlook.timezone="UTC" (no offset suffix, optional fraction).
func parseGraphTime(v dateTimeTimeZone) (time.Time, error) {
	s := v.DateTime
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return time.Time{}, fmt.Errorf("empty dateTime")
	}
	loc := time.UTC
	if v.TimeZone != "" && !strings.EqualFold(v.TimeZone, "UTC") {
		if l, err := time.LoadLocation(v.TimeZone); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, loc)
	return t.UTC(), err
}

// Event maps a Graph event payload onto the domain model, normalizing times
// to UTC and masking private events according to the policy.
func Event(raw json.RawMessage, calendarID string, priv PrivatePolicy) (domain.CalendarEvent, error) {
	var v struct {
		ID                    string           `json:"id"`
		ICalUID               string           `json:"iCalUId"`
		SeriesMasterID        string           `json:"seriesMasterId"`
		Type                  string           `json:"type"`
		Subject               string           `json:"subject"`
		BodyPreview           string           `json:"bodyPreview"`
		Start                 dateTimeTimeZone `json:"start"`
		End                   dateTimeTimeZone `json:"end"`
		OriginalStart         string           `json:"originalStart"`
		OriginalStartTimeZone string           `json:"originalStartTimeZone"`
		OriginalEndTimeZone   string           `json:"originalEndTimeZone"`
		IsAllDay              bool             `json:"isAllDay"`
		IsCancelled           bool             `json:"isCancelled"`
		IsOrganizer           bool             `json:"isOrganizer"`
		IsOnlineMeeting       bool             `json:"isOnlineMeeting"`
		HasAttachments        bool             `json:"hasAttachments"`
		ShowAs                string           `json:"showAs"`
		Importance            string           `json:"importance"`
		Sensitivity           string           `json:"sensitivity"`
		WebLink               string           `json:"webLink"`
		ETag                  string           `json:"@odata.etag"`
		Created               string           `json:"createdDateTime"`
		Modified              string           `json:"lastModifiedDateTime"`
		Categories            []string         `json:"categories"`
		OnlineMeetingURL      string           `json:"onlineMeetingUrl"`
		OnlineMeeting         struct {
			JoinURL string `json:"joinUrl"`
		} `json:"onlineMeeting"`
		Body struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		Organizer struct {
			EmailAddress struct {
				Address string `json:"address"`
				Name    string `json:"name"`
			} `json:"emailAddress"`
		} `json:"organizer"`
		Location struct {
			DisplayName string `json:"displayName"`
		} `json:"location"`
		Locations []struct {
			DisplayName string `json:"displayName"`
		} `json:"locations"`
		Attendees []struct {
			Type   string `json:"type"`
			Status struct {
				Response string `json:"response"`
			} `json:"status"`
			EmailAddress struct {
				Address string `json:"address"`
				Name    string `json:"name"`
			} `json:"emailAddress"`
		} `json:"attendees"`
		ResponseStatus struct {
			Response string `json:"response"`
		} `json:"responseStatus"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return domain.CalendarEvent{}, e
	}
	if v.ID == "" {
		return domain.CalendarEvent{}, fmt.Errorf("calendar event without id")
	}
	start, e := parseGraphTime(v.Start)
	if e != nil {
		return domain.CalendarEvent{}, fmt.Errorf("event %s start: %w", v.ID, e)
	}
	end, e := parseGraphTime(v.End)
	if e != nil {
		return domain.CalendarEvent{}, fmt.Errorf("event %s end: %w", v.ID, e)
	}
	eventType := v.Type
	if eventType == "" {
		eventType = "singleInstance"
	}
	startTZ := v.OriginalStartTimeZone
	if startTZ == "" {
		startTZ = v.Start.TimeZone
	}
	endTZ := v.OriginalEndTimeZone
	if endTZ == "" {
		endTZ = v.End.TimeZone
	}
	ev := domain.CalendarEvent{
		ID: v.ID, CalendarID: calendarID, ICalUID: v.ICalUID, SeriesMasterID: v.SeriesMasterID,
		EventType: eventType, Subject: v.Subject, BodyPreview: v.BodyPreview,
		StartUTC: start, EndUTC: end, StartTimezone: startTZ, EndTimezone: endTZ,
		OriginalStart: v.OriginalStart, IsAllDay: v.IsAllDay, IsCancelled: v.IsCancelled,
		IsOrganizer: v.IsOrganizer, IsOnlineMeeting: v.IsOnlineMeeting, HasAttachments: v.HasAttachments,
		OrganizerAddress: v.Organizer.EmailAddress.Address, OrganizerName: v.Organizer.EmailAddress.Name,
		LocationName: v.Location.DisplayName, ShowAs: v.ShowAs, Importance: v.Importance,
		Sensitivity: v.Sensitivity, ResponseStatus: v.ResponseStatus.Response,
		OnlineMeetingURL: v.OnlineMeetingURL, WebURL: v.WebLink, ETag: v.ETag,
		Categories: v.Categories, RawJSON: raw,
	}
	if strings.EqualFold(v.Body.ContentType, "html") {
		ev.BodyHTML = v.Body.Content
		ev.BodyText = text.PlainHTML(v.Body.Content)
	} else {
		ev.BodyText = v.Body.Content
	}
	// Graph online-meeting info is preferred over parsing the body
	switch {
	case v.OnlineMeeting.JoinURL != "":
		ev.JoinURL = v.OnlineMeeting.JoinURL
	case v.OnlineMeetingURL != "":
		ev.JoinURL = v.OnlineMeetingURL
	default:
		ev.JoinURL = teamsJoinURL.FindString(v.Body.Content)
	}
	for _, l := range v.Locations {
		if l.DisplayName != "" {
			ev.Locations = append(ev.Locations, l.DisplayName)
		}
	}
	for _, a := range v.Attendees {
		ev.Attendees = append(ev.Attendees, domain.Attendee{Type: a.Type, Address: a.EmailAddress.Address, Name: a.EmailAddress.Name, Response: a.Status.Response})
	}
	if t, err := time.Parse(time.RFC3339, v.Created); err == nil {
		ev.CreatedAt = &t
	}
	if t, err := time.Parse(time.RFC3339, v.Modified); err == nil {
		ev.ModifiedAt = &t
	}
	if strings.EqualFold(ev.Sensitivity, "private") && !priv.StoreDetails {
		mask(&ev)
	}
	return ev, nil
}

// mask strips details of a private event while keeping its times, organizer,
// and Outlook URL. RawJSON is redacted so the mask cannot be bypassed.
func mask(ev *domain.CalendarEvent) {
	ev.Subject = maskedSubject
	ev.BodyHTML, ev.BodyText, ev.BodyPreview = "", "", ""
	ev.LocationName, ev.OnlineMeetingURL, ev.JoinURL = "", "", ""
	ev.Attendees, ev.Locations, ev.Categories, ev.Attachments = nil, nil, nil, nil
	redacted, _ := json.Marshal(map[string]string{"id": ev.ID, "sensitivity": ev.Sensitivity, "type": ev.EventType})
	ev.RawJSON = redacted
}
