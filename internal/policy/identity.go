package policy

import (
	"fmt"
	"regexp"
	"strings"
)

var emailPattern = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

func NormalizeIdentity(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("identity is empty")
	}
	if strings.Contains(value, "@") {
		if strings.ContainsAny(value, "*?") || !emailPattern.MatchString(value) {
			return "", fmt.Errorf("identity %q is not an exact email address", raw)
		}
		return strings.ToLower(value), nil
	}
	var b strings.Builder
	for i, r := range value {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			continue
		default:
			return "", fmt.Errorf("identity %q is not an exact phone or email identity", raw)
		}
	}
	normalized := b.String()
	if !strings.HasPrefix(normalized, "+") || len(normalized) < 9 || len(normalized) > 16 || normalized[1] == '0' {
		return "", fmt.Errorf("phone identity %q must normalize to E.164", raw)
	}
	return normalized, nil
}
