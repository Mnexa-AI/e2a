package identity

import "testing"

func TestStoredInboundSender(t *testing.T) {
	tests := []struct {
		name string
		auth InboundAuth
		want string
	}{
		{
			name: "preserves explicit internal routing sender",
			auth: InboundAuth{HeaderFrom: "claimed@example.com", StoredSender: "reply@example.net"},
			want: "reply@example.net",
		},
		{
			name: "falls back to header from",
			auth: InboundAuth{HeaderFrom: "claimed@example.com"},
			want: "claimed@example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedInboundSender(tc.auth); got != tc.want {
				t.Fatalf("storedInboundSender() = %q, want %q", got, tc.want)
			}
		})
	}
}
