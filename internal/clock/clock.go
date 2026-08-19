// Package clock provides an injectable time source and a timezone-aware
// calendar date type used for puzzle dates.
package clock

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Clock is a time source. Production code uses Real; tests use Fake.
type Clock interface {
	Now() time.Time
}

// Real reports the actual current time.
type Real struct{}

// Now implements Clock.
func (Real) Now() time.Time { return time.Now() }

// Fake reports a fixed time.
type Fake struct{ T time.Time }

// Now implements Clock.
func (f Fake) Now() time.Time { return f.T }

const dateLayout = "2006-01-02"

// Date is a calendar date with no time-of-day and no timezone. It is the type
// used for puzzle dates everywhere in the application.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// PuzzleDateAt returns the calendar date that the instant t falls on in loc.
func PuzzleDateAt(t time.Time, loc *time.Location) Date {
	local := t.In(loc)
	return Date{Year: local.Year(), Month: local.Month(), Day: local.Day()}
}

// ParseDate parses an ISO-8601 date such as "2026-08-18".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// String renders the date as "2006-01-02".
func (d Date) String() string {
	return d.time().Format(dateLayout)
}

// AddDays returns the date n days after d. n may be negative.
func (d Date) AddDays(n int) Date {
	t := d.time().AddDate(0, 0, n)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// Before reports whether d falls before other.
func (d Date) Before(other Date) bool { return d.time().Before(other.time()) }

// Equal reports whether d and other are the same calendar date.
func (d Date) Equal(other Date) bool { return d == other }

// time renders the date as midnight UTC. It is only ever used for arithmetic
// and formatting, never as a real instant.
func (d Date) time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// Value implements driver.Valuer so a Date can be written to a Postgres date
// column.
func (d Date) Value() (driver.Value, error) { return d.time(), nil }

// Scan implements sql.Scanner so a Postgres date column can be read into a Date.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = Date{Year: v.Year(), Month: v.Month(), Day: v.Day()}
		return nil
	case string:
		parsed, err := ParseDate(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case nil:
		return fmt.Errorf("cannot scan NULL into clock.Date")
	default:
		return fmt.Errorf("cannot scan %T into clock.Date", src)
	}
}
