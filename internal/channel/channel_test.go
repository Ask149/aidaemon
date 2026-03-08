package channel

import "testing"

func TestSessionID(t *testing.T) {
	tests := []struct {
		channel, chatID, want string
	}{
		{"telegram", "12345", "telegram:12345"},
		{"websocket", "abc-def", "websocket:abc-def"},
	}
	for _, tt := range tests {
		got := SessionID(tt.channel, tt.chatID)
		if got != tt.want {
			t.Errorf("SessionID(%q, %q) = %q, want %q", tt.channel, tt.chatID, got, tt.want)
		}
	}
}
