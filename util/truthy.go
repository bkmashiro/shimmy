package util

import "strings"

func Truthy(s string) bool {
	normalized := strings.ToLower(strings.Trim(s, " "))
	return normalized == "true" || normalized == "1" || normalized == "yes"
}
