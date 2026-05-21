package glance

import (
    "encoding/json"
    "errors"
    "fmt"
    "html/template"
    "net/http"
    "time"

    ics "github.com/arran4/golang-ical"
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
    FirstDayOfWeek string             `yaml:"first-day-of-week"`
    Ics            []calendarIcsConfig `yaml:"ics"`
    FirstDay       int                `yaml:"-"`
    cachedHTML     template.HTML      `yaml:"-"`
    Events         string             `yaml:"events"`
}

type calendarEvent struct {
    Date    string `json:"Date"`
    EndDate string `json:"EndDate"`
    Name    string `json:"Name"`
    Color   string `json:"Color"`
}

func (widget *calendarWidget) initialize() error {
    widget.withTitle("Calendar").withError(nil)

    if widget.FirstDayOfWeek == "" {
        widget.FirstDayOfWeek = "monday"
    } else if _, ok := calendarWeekdaysToInt[widget.FirstDayOfWeek]; !ok {
        return errors.New("invalid first day of week")
    }

    var widgetEvents []calendarEvent
    for _, icsConfig := range widget.Ics {
        url := icsConfig.Url
        color := icsConfig.Color
        
        newEvents, err := ReadPublicIcs(url)
        if err != nil {
            fmt.Println(err)
        }

        for _, event := range newEvents {
            name := ""
            if p := event.GetProperty("SUMMARY"); p != nil {
                name = p.Value
            }

            e := calendarEvent{Name: name, Color: color}

            if start, err := event.GetStartAt(); err == nil {
                e.Date = start.Format(time.RFC3339)
            } else if start, err := event.GetAllDayStartAt(); err == nil {
                e.Date = start.Format("20060102")
            } else {
                continue
            }

            if end, err := event.GetEndAt(); err == nil {
                e.EndDate = end.Format(time.RFC3339)
            } else if end, err := event.GetAllDayEndAt(); err == nil {
                e.EndDate = end.Format("20060102")
            }

            widgetEvents = append(widgetEvents, e)
        }
    }

    jsonBytes, err := json.Marshal(widgetEvents)
    if err != nil {
        panic(err)
    }
    widget.Events = string(jsonBytes)
    widget.FirstDay = int(calendarWeekdaysToInt[widget.FirstDayOfWeek])
    widget.cachedHTML = widget.renderTemplate(widget, calendarWidgetTemplate)

    return nil
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