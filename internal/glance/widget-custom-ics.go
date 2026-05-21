package glance

import (
    "context"
    "encoding/json"
    "fmt"
    "html/template"
    "sort"
    "strings"
    "time"

    "github.com/tidwall/gjson"
)

var customIcsWidgetTemplate = mustParseTemplate("custom-api.html", "widget-base.html")

type customIcsConfig struct {
    Url   string `yaml:"url"`
    Color string `yaml:"color"`
}

type customIcsWidget struct {
    widgetBase        `yaml:",inline"`
    Ics               []customIcsConfig `yaml:"ics"`
    Template          string            `yaml:"template"`
    MaxEvents         int               `yaml:"max-events"`
    DaysAhead         int               `yaml:"days-ahead"`
    Frameless         bool              `yaml:"frameless"`
    compiledTemplate  *template.Template `yaml:"-"`
    CompiledHTML      template.HTML      `yaml:"-"`
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

    var events []map[string]interface{}

    for _, icsConf := range widget.Ics {
        calEvents, err := ReadPublicIcs(icsConf.Url)
        if err != nil {
            continue
        }

        for _, ev := range calEvents {
            summary := ""
            if p := ev.GetProperty("SUMMARY"); p != nil {
                summary = p.Value
            }

            isAllDay := false
            var start, end time.Time

            if s, err := ev.GetAllDayStartAt(); err == nil {
                start = s
                isAllDay = true
            } else if s, err := ev.GetStartAt(); err == nil {
                start = s
            } else {
                continue
            }

            if isAllDay {
                if e, err := ev.GetAllDayEndAt(); err == nil {
                    end = e
                } else {
                    end = start
                }
            } else {
                if e, err := ev.GetEndAt(); err == nil {
                    end = e
                } else {
                    end = start
                }
            }

            if isAllDay && !end.IsZero() {
                end = end.AddDate(0, 0, -1)
            }

            if end.After(now) && start.Before(inXDays) {
                if !isAllDay && start.Format("2006-01-02") != end.Format("2006-01-02") {
                    end = time.Date(start.Year(), start.Month(), start.Day(), end.Hour(), end.Minute(), 0, 0, start.Location())
                }

                var startIso, endIso string
                
                if isAllDay {
                    startIso = start.Format("2006-01-02")
                    endIso = end.Format("2006-01-02")
                } else {
                    startIso = start.Format(time.RFC3339)
                    endIso = end.Format(time.RFC3339)
                }

                // Calculate "in Xh Xm" directly in Go (Bypasses broken JS)
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

                events = append(events, map[string]interface{}{
                    "color":      icsConf.Color,
                    "summary":    summary,
                    "isAllDay":   isAllDay,
                    "startIso":   startIso,
                    "endIso":     endIso,
                    "hoursUntil": hoursUntil,
                    "_sort":      start,
                })
            }
        }
    }

    sort.Slice(events, func(i, j int) bool {
        return events[i]["_sort"].(time.Time).Before(events[j]["_sort"].(time.Time))
    })

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