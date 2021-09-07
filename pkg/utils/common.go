package utils

import "time"

func Expired(expiredAt time.Time) bool {
	return time.Now().After(expiredAt)
}

func ParseTimestamp(expiresAt string, errs *[]error) time.Time {
	expired, err := Parse(expiresAt)
	if err != nil {
		*errs = append(*errs, err)
	}
	return expired
}

func Parse(expiresAt string) (time.Time, error) {
	return time.Parse(time.RFC3339, expiresAt)
}
