package utils

import "strconv"

// ParseInt64OrDefault parses a decimal string to int64, returning defaultVal if parsing fails.
func ParseInt64OrDefault(s string, defaultVal int64) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return v
}
