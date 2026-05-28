import { directions, easeOutQuint, slideFade } from "./animations.js";
import { elem, repeat, text } from "./templating.js";

const FULL_MONTH_SLOTS = 7 * 6;
const WEEKDAY_ABBRS = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];
const MONTH_NAMES = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

const leftArrowSvg = `<svg stroke="var(--color-text-base)" fill="none" viewBox="0 0 24 24" stroke-width="1.5" xmlns="http://www.w3.org/2000/svg"><path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5" /></svg>`;
const rightArrowSvg = `<svg stroke="var(--color-text-base)" fill="none" viewBox="0 0 24 24" stroke-width="1.5" xmlns="http://www.w3.org/2000/svg"><path stroke-linecap="round" stroke-linejoin="round" d="m8.25 4.5 7.5 7.5-7.5 7.5" /></svg>`;
const undoArrowSvg = `<svg stroke="var(--color-text-base)" fill="none" viewBox="0 0 24 24" stroke-width="1.5" xmlns="http://www.w3.org/2000/svg"><path stroke-linecap="round" stroke-linejoin="round" d="M9 15 3 9m0 0 6-6M3 9h12a6 6 0 0 1 0 12h-3" /></svg>`;

const [datesExitLeft, datesExitRight] = directions(slideFade, { distance: "2rem", duration: 120, offset: 1 }, "left", "right");
const [datesEntranceLeft, datesEntranceRight] = directions(slideFade, { distance: "0.8rem", duration: 500, easing: easeOutQuint }, "left", "right");
const undoEntrance = slideFade({ direction: "left", distance: "100%", duration: 300 });

const tooltip = document.createElement("div");
tooltip.className = "calendar-event-tooltip";
tooltip.style.display = "none";
document.body.appendChild(tooltip);

export default function(element) {
    element.swapWith(Calendar(
        Number(element.dataset.firstDayOfWeek ?? 1),
        element.dataset.events
    ));
}

function Calendar(firstDay, events) {
    let header, dates;
    let advanceTimeTicker;
    let now = new Date();
    let activeDate;

    const update = (newDate) => {
        header.component.update(now, newDate);
        dates.component.update(now, newDate);
        activeDate = newDate;
    };

    const autoAdvanceNow = () => {
        advanceTimeTicker = setTimeout(() => {
            update(now = new Date());
            autoAdvanceNow();
        }, msTillNextDay());
    };

    const adjacentMonth = (dir) => new Date(activeDate.getFullYear(), activeDate.getMonth() + dir, 1);
    const nextClicked = () => update(adjacentMonth(1));
    const prevClicked = () => update(adjacentMonth(-1));
    const undoClicked = () => update(now);

    const calendar = elem().classes("calendar").append(
        header = Header(nextClicked, prevClicked, undoClicked),
        dates = Dates(firstDay, events)
    );

    update(now);
    autoAdvanceNow();

    return calendar.component({
        suspend: () => clearTimeout(advanceTimeTicker)
    });
}

function Header(nextClicked, prevClicked, undoClicked) {
    let month, monthNumber, year, undo;
    const button = () => elem("button").classes("calendar-header-button");

    const monthAndYear = elem().classes("size-h2", "color-highlight").append(
        month = text(), " ",
        year = elem("span").classes("size-h3"),
        undo = button().hide().classes("calendar-undo-button").attr("title", "Back to current month").on("click", undoClicked).html(undoArrowSvg)
    );

    const monthSwitcher = elem().classes("flex", "gap-7", "items-center").append(
        button().attr("title", "Previous month").on("click", prevClicked).html(leftArrowSvg),
        monthNumber = elem().classes("color-highlight").styles({ marginTop: "0.1rem" }),
        button().attr("title", "Next month").on("click", nextClicked).html(rightArrowSvg),
    );

    return elem().classes("flex", "justify-between", "items-center").append(monthAndYear, monthSwitcher).component({
        update: function (now, newDate) {
            month.text(MONTH_NAMES[newDate.getMonth()]);
            year.text(newDate.getFullYear());
            const m = newDate.getMonth() + 1;
            monthNumber.text((m < 10 ? "0" : "") + m);
            if (!datesWithinSameMonth(now, newDate)) { if (undo.isHidden()) undo.show().animate(undoEntrance); } else { undo.hide(); }
            return this;
        }
    });
}

function Dates(firstDay, events) {
    let dates, lastRenderedDate;

    const updateFullMonth = function(now, newDate) {
        const firstWeekday = new Date(newDate.getFullYear(), newDate.getMonth(), 1).getDay();
        const previousMonthSpilloverDays = (firstWeekday - firstDay + 7) % 7 || 7;
        const currentMonthDays = daysInMonth(newDate.getFullYear(), newDate.getMonth());
        const nextMonthSpilloverDays = FULL_MONTH_SLOTS - (previousMonthSpilloverDays + currentMonthDays);
        const previousMonthDays = daysInMonth(newDate.getFullYear(), newDate.getMonth() - 1);
        const isCurrentMonth = datesWithinSameMonth(now, newDate);
        const currentDate = now.getDate();

        let parsedEvents = null;
        if (events && events !== "null") {
            parsedEvents = JSON.parse(events);
        }

        let children = dates.children;
        let index = 0;

        // Renders a single day cell, checking for events regardless of whether
        // the day belongs to the current, previous, or next month.
        const renderDayCell = (child, dayNum, date, isSpillover, isToday) => {
            child.classesIf(isSpillover, "calendar-spillover-date");
            child.classesIf(isToday, "calendar-current-date");

            if (parsedEvents) {
                const dayEvents = getEventsForDateRaw(date, parsedEvents);
                if (dayEvents.length > 0) {
                    child.classes("calendar-event-date");
                    // Pass isSpillover to handle grayed out styling
                    child.html(buildDayMarkup(dayNum, dayEvents, isSpillover));
                    child.on("mouseenter", (e) => {
                        tooltip.innerHTML = getEventsForDateFormatted(date, parsedEvents).join("<br>");
                        tooltip.style.display = "block";
                        tooltip.style.left = e.pageX + 10 + "px";
                        tooltip.style.top = e.pageY + 10 + "px";
                    });
                    child.on("mousemove", (e) => {
                        tooltip.style.left = e.pageX + 10 + "px";
                        tooltip.style.top = e.pageY + 10 + "px";
                    });
                    child.on("mouseleave", () => {
                        tooltip.style.display = "none";
                    });
                    return;
                }
            }
            
            // Handle no-event case: Apply opacity style if spillover
            const spilloverStyle = isSpillover ? 'style="opacity: 0.5;"' : '';
            child.html(`<span class="calendar-day-text" ${spilloverStyle}>${dayNum}</span>`);
        };

        for (let i = 0; i < FULL_MONTH_SLOTS; i++) {
            children[i].clearClasses("calendar-spillover-date", "calendar-current-date", "calendar-event-date");
        }

        // Previous-month spillover days
        for (let i = 0; i < previousMonthSpilloverDays; i++, index++) {
            const dayNum = previousMonthDays - previousMonthSpilloverDays + i + 1;
            const date = new Date(newDate.getFullYear(), newDate.getMonth() - 1, dayNum);
            renderDayCell(children[index], dayNum, date, true, false);
        }

        // Current-month days
        for (let i = 1; i <= currentMonthDays; i++, index++) {
            const date = new Date(newDate.getFullYear(), newDate.getMonth(), i);
            renderDayCell(children[index], i, date, false, isCurrentMonth && i === currentDate);
        }

        // Next-month spillover days
        for (let i = 0; i < nextMonthSpilloverDays; i++, index++) {
            const dayNum = i + 1;
            const date = new Date(newDate.getFullYear(), newDate.getMonth() + 1, dayNum);
            renderDayCell(children[index], dayNum, date, true, false);
        }

        lastRenderedDate = newDate;
    };

    const update = function(now, newDate) {
        if (lastRenderedDate === undefined || datesWithinSameMonth(newDate, lastRenderedDate)) {
            updateFullMonth(now, newDate);
            return;
        }
        const next = newDate > lastRenderedDate;
        dates.animateUpdate(
            () => updateFullMonth(now, newDate),
            next ? datesExitLeft : datesExitRight,
            next ? datesEntranceRight : datesEntranceLeft,
        );
    };

    return elem().append(
        elem().classes("calendar-dates", "margin-top-15").append(
            ...repeat(7, (i) => elem().classes("size-h6", "color-subdue").text(WEEKDAY_ABBRS[(firstDay + i) % 7]))
        ),
        dates = elem().classes("calendar-dates", "margin-top-3").append(
            ...elem().classes("calendar-date").duplicate(FULL_MONTH_SLOTS)
        )
    ).component({ update });
}

// --- VISUAL MARKUP GENERATOR ---

function buildDayMarkup(dayNum, events, isSpillover) {
    // Define style string based on spillover status
    const dimStyle = isSpillover ? 'opacity: 0.5;' : '';
    
    let html = `<span class="calendar-day-text" style="${dimStyle}">${dayNum}</span>`;
    
    const timedColors = new Set();
    const allDayColors = []; // Array to allow multiple lines of the same color

    events.forEach(ev => {
        const color = ev.Color || "#6b7280"; 
        if (isAllDayEvent(ev)) {
            // Push to array so multiple full-day events split the bottom line
            allDayColors.push(color); 
        } else {
            // Add to set so short events only show one dot per color
            timedColors.add(color);   
        }
    });

    // Timed events (Left Dots)
    if (timedColors.size > 0) {
        html += `<div class="calendar-dots" style="${dimStyle}">`;
        for (const color of timedColors) {
            html += `<span class="calendar-dot" style="background-color: ${color};"></span>`;
        }
        html += `</div>`;
    }

    // All-Day events (Bottom Split Lines)
    if (allDayColors.length > 0) {
        html += `<div class="calendar-lines" style="${dimStyle}">`;
        for (const color of allDayColors) {
            html += `<span class="calendar-line" style="background-color: ${color};"></span>`;
        }
        html += `</div>`;
    }

    return html;
}

// --- DATE LOGIC HELPERS ---

function datesWithinSameMonth(d1, d2) {
    return d1.getFullYear() === d2.getFullYear() && d1.getMonth() === d2.getMonth();
}

function daysInMonth(year, month) {
    return new Date(year, month + 1, 0).getDate();
}

function msTillNextDay(now) {
    now = now || new Date();
    return 86_400_000 - (now.getMilliseconds() + now.getSeconds() * 1000 + now.getMinutes() * 60_000 + now.getHours() * 3_600_000);
}

function isAllDayEvent(event) {
    if (event.Date.length === 8) return true;
    const isStartMidnight = event.Date.includes("T00:00:00");
    const isEndMidnight = !event.EndDate || event.EndDate.includes("T00:00:00");
    return isStartMidnight && isEndMidnight;
}

function parseEventDate(dateStr) {
    if (!dateStr) return null;
    if (dateStr.length === 8) {
        return new Date(parseInt(dateStr.substr(0, 4)), parseInt(dateStr.substr(4, 2)) - 1, parseInt(dateStr.substr(6, 2)));
    } else if (dateStr.includes('T')) {
        const datePart = dateStr.split('T')[0];
        const parts = datePart.split('-');
        if (parts.length === 3) return new Date(parseInt(parts[0]), parseInt(parts[1]) - 1, parseInt(parts[2]));
    }
    return new Date(dateStr);
}

function isDateInRange(date, start, end) {
    if (!start) return false;
    const dateOnly = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
    const startOnly = new Date(start.getFullYear(), start.getMonth(), start.getDate()).getTime();
    const endOnly = end ? new Date(end.getFullYear(), end.getMonth(), end.getDate()).getTime() : startOnly;
    return dateOnly >= startOnly && dateOnly <= endOnly;
}

function getEventsForDateRaw(date, eventsObject) {
    return eventsObject.filter(ev => {
        const allDay = isAllDayEvent(ev);
        const eventStart = parseEventDate(ev.Date);
        let eventEnd = ev.EndDate ? parseEventDate(ev.EndDate) : eventStart;
        if (allDay && ev.EndDate) {
            eventEnd = new Date(eventEnd.getFullYear(), eventEnd.getMonth(), eventEnd.getDate() - 1);
        }
        return isDateInRange(date, eventStart, eventEnd);
    });
}

function getEventsForDateFormatted(date, eventsObject) {
    return getEventsForDateRaw(date, eventsObject).map(ev => {
        const color = ev.Color || "#6b7280";
        const dot = `<span style="color:${color}; margin-right: 6px;">●</span>`;
        
        if (isAllDayEvent(ev)) {
            return `${dot}${ev.Name}`;
        } else {
            const evDate = new Date(ev.Date);
            const hours = String(evDate.getHours()).padStart(2, '0');
            const minutes = String(evDate.getMinutes()).padStart(2, '0');
            return `${dot}${hours}:${minutes} - ${ev.Name}`;
        }
    });
}