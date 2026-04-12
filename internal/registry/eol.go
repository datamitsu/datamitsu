package registry

import "time"

// parseEOLField parses the EOL field from endoflife.date API responses.
// The field can be a bool (true/false), a date string ("2025-04-30"), or "false".
// Returns true if the release is end-of-life.
func parseEOLField(eol interface{}) bool {
	switch v := eol.(type) {
	case bool:
		return v
	case string:
		if v == "" || v == "false" {
			return false
		}
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return true
		}
		return !t.After(time.Now())
	default:
		return false
	}
}
