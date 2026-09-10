package filter

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	codeValuePattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])(?:[0-9][ -]?){4,8}(?:[^A-Za-z0-9]|$)`)
	codeWordsPattern = regexp.MustCompile(`(?i)\b(?:verification|security|authentication|login|sign[ -]?in|one[ -]?time|otp|2fa|mfa|passcode)\b.{0,40}\b(?:code|password|pin)?\b|\b(?:code|passcode|pin)\b.{0,24}(?:is|:)`)
	urlPattern       = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	authTextPattern  = regexp.MustCompile(`(?i)\b(?:sign[ -]?in|log[ -]?in|authenticate|verification|verify|magic[ -]?link|reset[ -]?password)\b`)
)

// Suppress reports whether text likely contains an authentication code or
// sign-in link. It deliberately favors withholding uncertain secrets.
func Suppress(text string) bool {
	if codeValuePattern.MatchString(text) && codeWordsPattern.MatchString(text) {
		return true
	}
	for _, match := range urlPattern.FindAllString(text, -1) {
		trimmed := strings.TrimRight(match, ".,;:!?)]}")
		u, err := url.Parse(trimmed)
		if err != nil || u.Hostname() == "" {
			continue
		}
		lowerURL := strings.ToLower(u.EscapedPath() + "?" + u.RawQuery)
		if authTextPattern.MatchString(text) || strings.Contains(lowerURL, "magic") ||
			strings.Contains(lowerURL, "signin") || strings.Contains(lowerURL, "sign-in") ||
			strings.Contains(lowerURL, "login") || strings.Contains(lowerURL, "verify") ||
			strings.Contains(lowerURL, "authenticate") || strings.Contains(lowerURL, "token=") {
			return true
		}
	}
	return false
}
