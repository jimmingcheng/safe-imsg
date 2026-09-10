package filter

import "testing"

func TestSuppress(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"Your verification code is 123456", true},
		{"123456 is your Apple ID code.", true},
		{"Your verification code is ABCD-EFGH", true},
		{"One-time\npassword: ABCDEF", true},
		{"https://example.com/lo%67in?to%6ben=secret", true},
		{"https://example.com/#/login?token=secret", true},
		{"OTP: 91 22 30. Do not share it.", true},
		{"Use this magic link: https://example.com/magic?t=secret", true},
		{"Sign in at https://example.com/session/abc", true},
		{"https://example.com/path?token=secret", true},
		{"The building code is changing next week", false},
		{"Can you review code 123?", false},
		{"Dinner at 7:30?", false},
		{"Read https://example.com/article", false},
	}
	for _, tc := range tests {
		if got := Suppress(tc.text); got != tc.want {
			t.Errorf("Suppress(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
