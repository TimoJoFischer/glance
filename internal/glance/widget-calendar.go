package glance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

var calendarWidgetTemplate = mustParseTemplate("calendar.html", "widget-base.html")

var calendarWeekdaysToInt = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

type calendarIcsConfig struct {
	Url   string `yaml:"url"`
	Color string `yaml:"color"`
}

type calendarWidget struct {
	widgetBase     `yaml:",inline"`
	FirstDayOfWeek string              `yaml:"first-day-of-week"`
	Ics            []calendarIcsConfig `yaml:"ics"`
	FirstDay       int                 `yaml:"-"`
	cachedHTML     template.HTML       `yaml:"-"`
	Events         string              `yaml:"events"`
}

type calendarEvent struct {
	Date    string `json:"Date"`
	EndDate string `json:"EndDate"`
	Name    string `json:"Name"`
	Color   string `json:"Color"`
}

// rruleExpansionMonths controls how far back and forward recurring events
// are expanded. Yearly events (birthdays etc.) need the full year look-back
// so "today − 13 months … today + 13 months" covers every case the calendar
// widget can ever display.
const rruleExpansionMonths = 13

// defaultCalendarCacheDuration is used when the user hasn't set a `cache:`
// value for this widget in glance.yaml.
const defaultCalendarCacheDuration = 30 * time.Minute

func (widget *calendarWidget) initialize() error {
	widget.withTitle("Calendar").withError(nil)

	if widget.FirstDayOfWeek == "" {
		widget.FirstDayOfWeek = "monday"
	} else if _, ok := calendarWeekdaysToInt[widget.FirstDayOfWeek]; !ok {
		return errors.New("invalid first day of week")
	}

	widget.FirstDay = int(calendarWeekdaysToInt[widget.FirstDayOfWeek])

	// THE FIX: register this widget with the scheduler's cache/update
	// mechanism. Without this call, cacheType stays at its zero value
	// (cacheTypeInfinite), so widgetBase.requiresUpdate() always returns
	// false and the scheduler never calls update() again after the first
	// render - which is exactly why the calendar widget was frozen at
	// whatever it fetched on process start, regardless of the `cache:`
	// duration set in glance.yaml.
	widget.withCacheDuration(defaultCalendarCacheDuration)

	widget.fetchAndRender()

	return nil
}

// update is called periodically by the widget scheduler once requiresUpdate
// reports that cacheDuration has elapsed (see widget.go). This is the piece
// that was entirely missing before - calendarWidget only had initialize(),
// so it silently used widgetBase's no-op default update() and never
// refreshed.
func (widget *calendarWidget) update(ctx context.Context) {
	widget.fetchAndRender()
	widget.scheduleNextUpdate()
}

// fetchAndRender re-fetches all configured ICS feeds, expands recurring
// events, and re-renders the widget's cached HTML. Used by both initialize
// (first load) and update (subsequent scheduled refreshes) so the two paths
// can't drift apart.
func (widget *calendarWidget) fetchAndRender() {
	now := time.Now()
	rangeStart := now.AddDate(0, -rruleExpansionMonths, 0)
	rangeEnd := now.AddDate(0, rruleExpansionMonths, 0)

	var widgetEvents []calendarEvent

	for _, icsConfig := range widget.Ics {
		rawEvents, err := ReadPublicIcs(icsConfig.Url)
		if err != nil {
			fmt.Println(err)
			continue
		}

		for _, event := range rawEvents {
			expanded := expandEvent(event, icsConfig.Color, rangeStart, rangeEnd)
			widgetEvents = append(widgetEvents, expanded...)
		}
	}

	jsonBytes, err := json.Marshal(widgetEvents)
	if err != nil {
		fmt.Println("calendar: failed to marshal events:", err)
		return
	}

	widget.Events = string(jsonBytes)
	widget.cachedHTML = widget.renderTemplate(widget, calendarWidgetTemplate)
}

// expandEvent turns a single VEvent into one or more calendarEvents.
// For non-recurring events it returns exactly one entry.
// For events with an RRULE it expands every occurrence that falls inside
// [rangeStart, rangeEnd].
func expandEvent(event *ics.VEvent, color string, rangeStart, rangeEnd time.Time) []calendarEvent {
	name := ""
	if p := event.GetProperty("SUMMARY"); p != nil {
		name = p.Value
	}

	// --- determine start time and whether this is an all-day event ---
	var startTime time.Time
	isAllDay := false

	if start, err := event.GetStartAt(); err == nil {
		startTime = start
	} else if start, err := event.GetAllDayStartAt(); err == nil {
		startTime = start
		isAllDay = true
	} else {
		// Can't determine start — skip entirely.
		return nil
	}

	// --- determine duration ---
	var duration time.Duration
	if end, err := event.GetEndAt(); err == nil {
		duration = end.Sub(startTime)
	} else if end, err := event.GetAllDayEndAt(); err == nil {
		duration = end.Sub(startTime)
	}

	// Helper that formats a single occurrence as a calendarEvent.
	makeEvent := func(occ time.Time) calendarEvent {
		e := calendarEvent{Name: name, Color: color}
		occEnd := occ.Add(duration)
		if isAllDay {
			e.Date = occ.Format("20060102")
			if duration > 0 {
				e.EndDate = occEnd.Format("20060102")
			}
		} else {
			e.Date = occ.Format(time.RFC3339)
			if duration > 0 {
				e.EndDate = occEnd.Format(time.RFC3339)
			}
		}
		return e
	}

	// --- no RRULE: single event ---
	rruleProp := event.GetProperty("RRULE")
	if rruleProp == nil {
		// Still check whether the event overlaps the display window.
		eventEnd := startTime.Add(duration)
		if eventEnd.Before(rangeStart) || startTime.After(rangeEnd) {
			return nil
		}
		return []calendarEvent{makeEvent(startTime)}
	}

	// --- RRULE present: parse and expand ---
	//
	// StrToROption understands the value part of an RRULE property, e.g.
	//   "FREQ=WEEKLY;WKST=MO;COUNT=10;INTERVAL=1;BYDAY=TU,FR"
	// We then override Dtstart with the parsed start time so that timezone
	// and time-of-day are preserved correctly.
	rOption, err := rrule.StrToROption(rruleProp.Value)
	if err != nil {
		fmt.Printf("calendar: failed to parse RRULE %q for event %q: %v\n",
			rruleProp.Value, name, err)
		// Fall back to the base event.
		return []calendarEvent{makeEvent(startTime)}
	}
	rOption.Dtstart = startTime

	r, err := rrule.NewRRule(*rOption)
	if err != nil {
		fmt.Printf("calendar: failed to build RRule for event %q: %v\n", name, err)
		return []calendarEvent{makeEvent(startTime)}
	}

	occurrences := r.Between(rangeStart, rangeEnd, true /* inclusive */)

	var results []calendarEvent
	for _, occ := range occurrences {
		results = append(results, makeEvent(occ))
	}
	return results
}

func (widget *calendarWidget) Render() template.HTML {
	return widget.cachedHTML
}

func ReadPublicIcs(url string) ([]*ics.VEvent, error) {
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	cal, err := ics.ParseCalendar(response.Body)
	if err != nil {
		return nil, err
	}
	return cal.Events(), nil
}