package phone

import (
	"errors"
	"strings"
)

// Normalize checks an E.164 number and returns it unchanged aside from trimming.
func Normalize(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 16 || !strings.HasPrefix(value, "+") {
		return "", errors.New("phone number must be E.164, for example +18005550100")
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return "", errors.New("phone number must be E.164, for example +18005550100")
		}
	}
	return value, nil
}
