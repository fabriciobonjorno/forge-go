package jobs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	values   []bool
	wildcard bool
}

type cronSchedule struct {
	minute     cronField
	hour       cronField
	dayOfMonth cronField
	month      cronField
	dayOfWeek  cronField
}

func parseCron(expression string) (*cronSchedule, error) {
	if len(expression) > 256 {
		return nil, errors.New("cron expression is too long")
	}
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return nil, errors.New("cron expression must have 5 numeric fields: minute hour day-of-month month day-of-week")
	}
	minute, err := parseCronField(parts[0], 0, 59, false)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hour, err := parseCronField(parts[1], 0, 23, false)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	dayOfMonth, err := parseCronField(parts[2], 1, 31, false)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	month, err := parseCronField(parts[3], 1, 12, false)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dayOfWeek, err := parseCronField(parts[4], 0, 7, true)
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	schedule := &cronSchedule{
		minute: minute, hour: hour, dayOfMonth: dayOfMonth,
		month: month, dayOfWeek: dayOfWeek,
	}
	if _, err := schedule.nextRun(time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return nil, err
	}
	return schedule, nil
}

func parseCronField(expression string, minValue, maxValue int, dayOfWeek bool) (cronField, error) {
	field := cronField{values: make([]bool, maxValue+1)}
	if expression == "" {
		return cronField{}, errors.New("field is empty")
	}
	for _, part := range strings.Split(expression, ",") {
		if part == "" {
			return cronField{}, errors.New("empty list item")
		}
		step := 1
		base := part
		if before, after, hasStep := strings.Cut(part, "/"); hasStep {
			base = before
			parsed, err := strconv.Atoi(after)
			if err != nil || parsed < 1 {
				return cronField{}, errors.New("step must be a positive integer")
			}
			step = parsed
			if strings.Contains(after, "/") {
				return cronField{}, errors.New("field has more than one step")
			}
		}

		start, end := minValue, maxValue
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			left, right, _ := strings.Cut(base, "-")
			parsedStart, startErr := strconv.Atoi(left)
			parsedEnd, endErr := strconv.Atoi(right)
			if startErr != nil || endErr != nil {
				return cronField{}, errors.New("range endpoints must be integers")
			}
			start, end = parsedStart, parsedEnd
		case strings.ContainsAny(base, "*/,"):
			return cronField{}, errors.New("invalid field syntax")
		default:
			parsed, err := strconv.Atoi(base)
			if err != nil {
				return cronField{}, errors.New("value must be an integer, range, or wildcard")
			}
			start, end = parsed, parsed
			if step > 1 || strings.Contains(part, "/") {
				end = maxValue
			}
		}
		if start < minValue || end > maxValue || end < start {
			return cronField{}, fmt.Errorf("value must be between %d and %d with an ascending range", minValue, maxValue)
		}
		for value := start; value <= end; value += step {
			field.values[value] = true
		}
	}

	field.wildcard = true
	for value := minValue; value <= maxValue; value++ {
		if dayOfWeek && value == 7 {
			continue // 7 is an alias for Sunday (0).
		}
		if !field.values[value] {
			field.wildcard = false
			break
		}
	}
	return field, nil
}

func (f cronField) matches(value int) bool {
	return value >= 0 && value < len(f.values) && f.values[value]
}

func (c *cronSchedule) matchesDate(day time.Time) bool {
	if !c.month.matches(int(day.Month())) {
		return false
	}
	monthDayMatch := c.dayOfMonth.matches(day.Day())
	weekday := int(day.Weekday())
	weekdayMatch := c.dayOfWeek.matches(weekday) || weekday == 0 && c.dayOfWeek.matches(7)
	switch {
	case c.dayOfMonth.wildcard && c.dayOfWeek.wildcard:
		return true
	case c.dayOfMonth.wildcard:
		return weekdayMatch
	case c.dayOfWeek.wildcard:
		return monthDayMatch
	default:
		// Cron uses OR when both day-of-month and day-of-week are restricted.
		return monthDayMatch || weekdayMatch
	}
}

func (c *cronSchedule) nextRun(after time.Time) (time.Time, error) {
	start := after.UTC()
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	limit := day.AddDate(8, 0, 0)
	for date := day; date.Before(limit); date = date.AddDate(0, 0, 1) {
		if !c.matchesDate(date) {
			continue
		}
		for hour := 0; hour < 24; hour++ {
			if !c.hour.matches(hour) {
				continue
			}
			for minute := 0; minute < 60; minute++ {
				if !c.minute.matches(minute) {
					continue
				}
				candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, time.UTC)
				if candidate.After(start) {
					return candidate, nil
				}
			}
		}
	}
	return time.Time{}, errors.New("cron expression has no run in the next 8 years")
}

func nextCronRun(expression string, after time.Time) (time.Time, error) {
	schedule, err := parseCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.nextRun(after)
}
