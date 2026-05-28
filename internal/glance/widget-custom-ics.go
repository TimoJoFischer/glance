package glance

import (
    "context"
    "encoding/json"
    "fmt"
    "html/template"
    "math"
    "sort"
    "strings"
    "time"

    ics "github.com/arran4/golang-ical"
    "github.com/teambition/rrule-go"
    "github.com/tidwall/gjson"
)

var customIcsWidgetTemplate = mustParseTemplate("custom-api.html", "widget-base.html")

type customIcsConfig struct {
    Url   string `yaml:"url"`
    Color string `yaml:"color"`
}

type customIcsWidget struct {
    widgetBase       `yaml:",inline"`
    Ics              []customIcsConfig  `yaml:"ics"`
    Template         string             `yaml:"template"`
    MaxEvents        int                `yaml:"max-events"`
    DaysAhead        int                `yaml:"days-ahead"`
    Frameless        bool               `yaml:"frameless"`
    compiledTemplate *template.Template `yaml:"-"`
    CompiledHTML     template.HTML      `yaml:"-"`
}

func (widget *customIcsWidget) initialize() error {
    widget.withTitle("Custom ICS")
    widget.withCacheDuration(15 * time.Minute)

    if widget.Template == "" {
        return fmt.Errorf("template is required")
    }

    compiledTemplate, err := template.New("").Funcs(customAPITemplateFuncs).Parse(widget.Template)
    if err != nil {
        return fmt.Errorf("parsing template: %w", err)
    }

    widget.compiledTemplate = compiledTemplate
    return nil
}

// expandedIcsEvent represents a single expanded occurrence of an ICS event.
type expandedIcsEvent struct {
    Summary  string
    IsAllDay bool
    Start    time.Time
    End      time.Time
    Duration time.Duration
    Color    string
}

// expandIcsEvent turns a single VEvent into one or more expandedIcsEvents.
// For non-recurring events it returns exactly one entry.
// For events with an RRULE (weekly, yearly, daily, monthly…) it expands every
// occurrence that falls inside [rangeStart, rangeEnd], preserving the original
// duration and all-day flag for each occurrence.
func expandIcsEvent(event *ics.VEvent, color string, rangeStart, rangeEnd time.Time) []expandedIcsEvent {
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
        return nil
    }

    // --- determine duration ---
    var duration time.Duration
    if end, err := event.GetEndAt(); err == nil {
        duration = end.Sub(startTime)
    } else if end, err := event.GetAllDayEndAt(); err == nil {
        duration = end.Sub(startTime)
    }

    // Helper that builds an expandedIcsEvent for a single occurrence.
    makeExpanded := func(occ time.Time) expandedIcsEvent {
        return expandedIcsEvent{
            Summary:  name,
            IsAllDay: isAllDay,
            Start:    occ,
            End:      occ.Add(duration),
            Duration: duration,
            Color:    color,
        }
    }

    // --- no RRULE: single event ---
    rruleProp := event.GetProperty("RRULE")
    if rruleProp == nil {
        eventEnd := startTime.Add(duration)
        // Only include if the event overlaps the expansion window.
        if eventEnd.Before(rangeStart) || startTime.After(rangeEnd) {
            return nil
        }
        return []expandedIcsEvent{makeExpanded(startTime)}
    }

    // --- RRULE present: parse and expand ---
    rOption, err := rrule.StrToROption(rruleProp.Value)
    if err != nil {
        fmt.Printf("custom-ics: failed to parse RRULE %q for event %q: %v\n",
            rruleProp.Value, name, err)
        // Fall back to the base event.
        return []expandedIcsEvent{makeExpanded(startTime)}
    }
    rOption.Dtstart = startTime

    r, err := rrule.NewRRule(*rOption)
    if err != nil {
        fmt.Printf("custom-ics: failed to build RRule for event %q: %v\n", name, err)
        return []expandedIcsEvent{makeExpanded(startTime)}
    }

    occurrences := r.Between(rangeStart, rangeEnd, true /* inclusive */)

    var results []expandedIcsEvent
    for _, occ := range occurrences {
        results = append(results, makeExpanded(occ))
    }
    return results
}

func (widget *customIcsWidget) update(ctx context.Context) {
    defer func() {
        if r := recover(); r != nil {
            widget.withError(fmt.Errorf("template panic: %v", r))
        }
    }()

    now := time.Now()

    daysAhead := 7
    if widget.DaysAhead > 0 {
        daysAhead = widget.DaysAhead
    }
    inXDays := now.Add(time.Duration(daysAhead) * 24 * time.Hour)

    // Expansion window for recurring events.
    // Look back 90 days so that ongoing multi-day events and the current
    // occurrence of long-running recurring series are found by the rrule
    // expansion.  Events are filtered against now / inXDays afterwards.
    rangeStart := now.AddDate(0, 0, -90)
    rangeEnd := inXDays

    var events []map[string]interface{}

    for _, icsConf := range widget.Ics {
        calEvents, err := ReadPublicIcs(icsConf.Url)
        if err != nil {
            continue
        }

        for _, ev := range calEvents {
            // Expand recurring events (weekly, yearly, daily, monthly …)
            expanded := expandIcsEvent(ev, icsConf.Color, rangeStart, rangeEnd)

            for _, ex := range expanded {
                start := ex.Start
                end := ex.End
                isAllDay := ex.IsAllDay

                // Adjust all-day end date: ICS stores end as the day after
                // the last day, so shift back by one.
                if isAllDay && !end.IsZero() {
                    end = end.AddDate(0, 0, -1)
                }

                // Filter: event must still be ongoing or upcoming.
                if !end.After(now) {
                    continue
                }
                // Event must start within our look-ahead window.
                if !start.Before(inXDays) {
                    continue
                }

                var startIso, endIso string

                if isAllDay {
                    startIso = start.Format("2006-01-02")
                    endIso = end.Format("2006-01-02")
                } else {
                    startIso = start.Format(time.RFC3339)
                    endIso = end.Format(time.RFC3339)
                }

                // Calculate human-readable "time until start".
                hoursUntil := ""
                diff := start.Sub(now)
                if diff > 0 {
                    hours := int(diff.Hours())
                    minutes := int(diff.Minutes()) % 60
                    if hours > 24 {
                        days := hours / 24
                        hoursUntil = fmt.Sprintf("in %dd", days)
                    } else if hours > 0 {
                        if minutes > 0 {
                            hoursUntil = fmt.Sprintf("in %dh %dm", hours, minutes)
                        } else {
                            hoursUntil = fmt.Sprintf("in %dh", hours)
                        }
                    } else {
                        hoursUntil = fmt.Sprintf("in %dm", minutes)
                    }
                } else {
                    hoursUntil = "now"
                }

                // Calculate duration in hours
                durationHours := end.Sub(start).Hours()
                if durationHours < 0 {
                    durationHours = 0
                }
                var durationStr string
                if durationHours == math.Trunc(durationHours) {
                    // Whole numbers: 2h, 48h
                    durationStr = fmt.Sprintf("%dh", int(durationHours))
                } else {
                    // Decimals rounded to 1 place: 0.5h, 1.5h, 24.5h
                    durationStr = fmt.Sprintf("%.1fh", math.Round(durationHours*10)/10)
                }

                events = append(events, map[string]interface{}{
                    "color":      icsConf.Color,
                    "summary":    ex.Summary,
                    "isAllDay":   isAllDay,
                    "startIso":   startIso,
                    "endIso":     endIso,
                    "hoursUntil": hoursUntil,
                    "duration":   durationStr,
                    "_sort":      start,
                })
            }
        }
    }

    // Sort all events by start time.
    sort.Slice(events, func(i, j int) bool {
        return events[i]["_sort"].(time.Time).Before(events[j]["_sort"].(time.Time))
    })

    // Remove internal sort key before rendering.
    for _, e := range events {
        delete(e, "_sort")
    }

    if widget.MaxEvents > 0 && len(events) > widget.MaxEvents {
        events = events[:widget.MaxEvents]
    }

    if events == nil {
        events = []map[string]interface{}{}
    }

    jsonBytes, err := json.Marshal(map[string]interface{}{"events": events})
    if err != nil {
        widget.withError(err)
        return
    }

    parsedJson := gjson.Parse(string(jsonBytes))
    data := customAPITemplateData{
        customAPIResponseData: &customAPIResponseData{
            JSON: decoratedGJSONResult{parsedJson},
        },
        subrequests: make(map[string]*customAPIResponseData),
    }

    var templateBuffer strings.Builder
    err = widget.compiledTemplate.Execute(&templateBuffer, &data)
    if err != nil {
        widget.withError(err)
        return
    }

    widget.ContentAvailable = true
    widget.CompiledHTML = template.HTML(templateBuffer.String())
}

func (widget *customIcsWidget) Render() template.HTML {
    return widget.renderTemplate(widget, customIcsWidgetTemplate)
}