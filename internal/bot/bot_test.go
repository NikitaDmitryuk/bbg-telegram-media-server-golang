package bot

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeBotErrorRemovesTokenAndProxy(t *testing.T) {
	token := "123456:secret-token"
	proxy := "socks5://user:password@proxy.example:1080"
	err := errors.New("Post \"https://api.telegram.org/bot" + token + "/getMe\": proxy " + proxy + " failed")

	safeErr := sanitizeBotError(err, token, proxy)
	if safeErr == nil {
		t.Fatal("sanitizeBotError returned nil")
	}
	for _, secret := range []string{token, "secret-token", proxy, "user:password"} {
		if strings.Contains(safeErr.Error(), secret) {
			t.Fatalf("sanitized error contains %q: %s", secret, safeErr)
		}
	}
	if !strings.Contains(safeErr.Error(), "<redacted>") {
		t.Fatalf("sanitized error does not contain redaction marker: %s", safeErr)
	}
}
