package safehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateURLRejectsUnsafeDestinations(t *testing.T) {
	bad := []string{
		"http://127.0.0.1/",
		"http://127.0.0.1:8080/admin",
		"http://localhost:8080/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://0.0.0.0/",
		"file:///etc/passwd",
		"gopher://example.com/",
		"http://user:pass@example.com/",
		"http://",
	}
	for _, raw := range bad {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want an error", raw)
		}
	}
}

func TestValidateURLAcceptsAPublicHost(t *testing.T) {
	if err := ValidateURL("https://93.184.216.34/hook"); err != nil {
		t.Errorf("public literal IP rejected: %v", err)
	}
}

func TestClientRefusesToDialLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv.Close()

	// The server is on 127.0.0.1, so the dialer must refuse even though the
	// URL is handed straight to the client.
	if _, err := Client(5 * time.Second).Get(srv.URL); err == nil {
		t.Fatal("client connected to a loopback address")
	}
}
