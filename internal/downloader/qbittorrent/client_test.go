package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const qbittorrentLoginPath = "/api/v2/auth/login"

func TestClientCheckLoginSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qbittorrentLoginPath:
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v5.0.0"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	version, err := client.CheckLogin(context.Background())
	if err != nil {
		t.Fatalf("CheckLogin: %v", err)
	}
	if version != "v5.0.0" {
		t.Fatalf("version = %q, want v5.0.0", version)
	}
}

func TestTorrentsInfoReauthenticatesOnce(t *testing.T) {
	t.Parallel()
	var infoCalls atomic.Int32
	var loginCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qbittorrentLoginPath:
			loginCalls.Add(1)
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			if infoCalls.Add(1) == 1 {
				http.Error(w, "secret response", http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`[{"hash":"abc","state":"downloading"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	info, err := client.TorrentsInfo(context.Background(), "abc", "", false)
	if err != nil {
		t.Fatalf("TorrentsInfo: %v", err)
	}
	if len(info) != 1 || info[0].Hash != "abc" || loginCalls.Load() != 1 || infoCalls.Load() != 2 {
		t.Fatalf("unexpected calls/result: info=%+v login=%d info_calls=%d", info, loginCalls.Load(), infoCalls.Load())
	}
}

func TestClientErrorsDoNotExposeResponseBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sensitive server detail", http.StatusBadGateway)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.TorrentsInfo(context.Background(), "", "", false)
	if err == nil || strings.Contains(err.Error(), "sensitive server detail") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
}

func TestClientCheckLoginSuccessNoContent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qbittorrentLoginPath:
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v5.2.0"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	version, err := client.CheckLogin(context.Background())
	if err != nil {
		t.Fatalf("CheckLogin: %v", err)
	}
	if version != "v5.2.0" {
		t.Fatalf("version = %q, want v5.2.0", version)
	}
}

func TestClientLoginInvalidCredentials(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Fails."))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "bad")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(context.Background()); err == nil {
		t.Fatal("Login succeeded, want invalid credentials error")
	}
}

func TestClientLoginForbidden(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "banned", http.StatusForbidden)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(context.Background()); err == nil {
		t.Fatal("Login succeeded, want forbidden error")
	}
}

func TestClientLoginNonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(context.Background()); err == nil {
		t.Fatal("Login succeeded, want non-OK error")
	}
}

func TestClientLoginConnectionRefused(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	client, err := NewClient(url, "admin", "adminadmin")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(context.Background()); err == nil {
		t.Fatal("Login succeeded, want connection error")
	}
}
