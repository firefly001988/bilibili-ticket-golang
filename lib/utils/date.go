package utils

import "time"

// IsNextDayInCST checks whether target date is the next day relative to from date,
// using China Standard Time (Asia/Shanghai).
func IsNextDayInCST(from time.Time, target time.Time) bool {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := from.In(loc)
	afterHour := target.In(loc)
	return now.Format("20060102") != afterHour.Format("20060102")
}
