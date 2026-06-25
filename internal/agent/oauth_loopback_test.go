package agent

import "testing"

func TestIsLoopbackRedirect(t *testing.T) {
	cases := []struct {
		uri  string
		want bool
	}{
		// loopback http → true (the account-scope gate passes)
		{"http://localhost/callback", true},
		{"http://localhost:64793/callback", true},
		{"http://127.0.0.1/callback", true},
		{"http://127.0.0.1:8080/cb", true},
		{"http://[::1]/callback", true},
		{"http://[::1]:5173/cb", true},
		{"http://127.0.0.2/cb", true}, // all of 127.0.0.0/8 is loopback

		// https → false even if host is localhost (no guarantee the
		// callback lands on the user's machine)
		{"https://localhost/callback", false},
		{"https://app.example.com/callback", false},

		// non-loopback http → false
		{"http://example.com/callback", false},
		{"http://0.0.0.0/callback", false},
		{"http://10.0.0.5/callback", false},
		{"http://169.254.169.254/callback", false}, // link-local metadata, not loopback

		// junk → false (fail closed)
		{"", false},
		{"::::not a uri", false},
		{"javascript:alert(1)", false},
		{"file:///etc/passwd", false},
	}
	for _, c := range cases {
		if got := isLoopbackRedirect(c.uri); got != c.want {
			t.Errorf("isLoopbackRedirect(%q) = %v, want %v", c.uri, got, c.want)
		}
	}
}
