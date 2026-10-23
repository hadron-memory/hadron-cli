package calendar

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type infoDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	TimeZone    *string `json:"timeZone"`
	IsPrimary   bool    `json:"isPrimary"`
	CanEdit     bool    `json:"canEdit"`
}
type boundaryDTO struct {
	Date     *string `json:"date"`
	DateTime *string `json:"dateTime"`
	TimeZone *string `json:"timeZone"`
}
type personDTO struct {
	Address *string `json:"address"`
	Name    *string `json:"name"`
}
type attendeeDTO struct {
	Address  string  `json:"address"`
	Name     *string `json:"name"`
	Optional bool    `json:"optional"`
	Response string  `json:"response"`
}
type eventDTO struct {
	ID          string        `json:"id"`
	CalendarID  string        `json:"calendarId"`
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Location    *string       `json:"location"`
	Start       boundaryDTO   `json:"start"`
	End         boundaryDTO   `json:"end"`
	IsAllDay    bool          `json:"isAllDay"`
	Status      string        `json:"status"`
	Organizer   *personDTO    `json:"organizer"`
	Attendees   []attendeeDTO `json:"attendees"`
	WebLink     *string       `json:"webLink"`
	SeriesID    *string       `json:"seriesId"`
	IsOrganizer bool          `json:"isOrganizer"`
	MyResponse  *string       `json:"myResponse"`
	CreatedAt   *string       `json:"createdAt"`
	UpdatedAt   *string       `json:"updatedAt"`
}
type busyDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
type scheduleDTO struct {
	ID    string    `json:"id"`
	Error *string   `json:"error"`
	Busy  []busyDTO `json:"busy"`
}

func protocol() error {
	return exitcode.Newf(exitcode.Unavailable, "server returned an incomplete calendar response")
}

// Project data only: all-day dates, timed instants and zones retain independent nullable fields.
func projectEvent(v gen.CalendarReadEvent) (eventDTO, error) {
	if v.Start == nil || v.End == nil || v.Attendees == nil {
		return eventDTO{}, protocol()
	}
	r := eventDTO{ID: v.Id, CalendarID: v.CalendarId, Title: v.Title, Description: v.Description, Location: v.Location, Start: boundaryDTO{v.Start.Date, v.Start.DateTime, v.Start.TimeZone}, End: boundaryDTO{v.End.Date, v.End.DateTime, v.End.TimeZone}, IsAllDay: v.IsAllDay, Status: v.Status, Attendees: []attendeeDTO{}, WebLink: v.WebLink, SeriesID: v.SeriesId, IsOrganizer: v.IsOrganizer, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.Organizer != nil {
		r.Organizer = &personDTO{v.Organizer.Address, v.Organizer.Name}
	}
	if v.MyResponse != nil {
		s := string(*v.MyResponse)
		r.MyResponse = &s
	}
	for _, a := range v.Attendees {
		if a == nil {
			return eventDTO{}, protocol()
		}
		r.Attendees = append(r.Attendees, attendeeDTO{a.Address, a.Name, a.Optional, string(a.Response)})
	}
	return r, nil
}
