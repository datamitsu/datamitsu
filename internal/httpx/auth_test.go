package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDownloadAuthScopesAndRedirect(t *testing.T) {
	t.Setenv("TEST_DOWNLOAD_TOKEN", "secret")
	end := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Private-Token") != "" {
			t.Error("token leaked")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer end.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Private-Token") != "secret" {
			t.Error("token missing")
		}
		http.Redirect(w, r, end.URL, http.StatusFound)
	}))
	defer start.Close()
	auth := &RequestAuth{TokenEnv: "TEST_DOWNLOAD_TOKEN", Origin: start.URL, Header: "PRIVATE-TOKEN"}
	client, err := WithAuth(NewHardenedClient(time.Second), auth)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, start.URL, nil)
	if err := auth.Apply(req); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, ref := range []*RequestAuth{{TokenEnv: "DATAMITSU_TOKEN", Origin: start.URL, Header: "PRIVATE-TOKEN"}, {TokenEnv: "TEST_DOWNLOAD_TOKEN", Origin: "https://example.test/path", Header: "PRIVATE-TOKEN"}, {TokenEnv: "TEST_DOWNLOAD_TOKEN", Origin: start.URL, Header: "Cookie"}} {
		if ref.Validate() == nil {
			t.Fatal("unsafe auth accepted")
		}
	}
}

func TestAuthDefaultPortsAreSameOrigin(t *testing.T) {
	t.Setenv("TEST_DOWNLOAD_TOKEN", "credential")
	for _, pair := range [][2]string{{"https://forge.example.test", "https://forge.example.test:443/file"}, {"https://FORGE.example.test:443", "https://forge.example.test/file"}, {"http://localhost", "http://localhost:80/file"}} {
		auth := &RequestAuth{TokenEnv: "TEST_DOWNLOAD_TOKEN", Origin: pair[0], Header: "PRIVATE-TOKEN"}
		req, err := http.NewRequest(http.MethodGet, pair[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := auth.Apply(req); err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("Private-Token") != "credential" {
			t.Fatalf("same-origin request lacked token: %v", pair)
		}
	}
}

func TestAuthMissingCredentialFailsOnlyOnBoundOrigin(t *testing.T) {
	t.Setenv("TEST_DOWNLOAD_TOKEN", "")
	auth := &RequestAuth{TokenEnv: "TEST_DOWNLOAD_TOKEN", Origin: "https://forge.example.test", Header: "Authorization", Scheme: "Bearer"}
	req, err := http.NewRequest(http.MethodGet, "https://forge.example.test/binary", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Apply(req); err == nil || !strings.Contains(err.Error(), "TEST_DOWNLOAD_TOKEN") {
		t.Fatalf("missing credential error=%v", err)
	}
	other, err := http.NewRequest(http.MethodGet, "https://cdn.example.test/binary", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Apply(other); err != nil {
		t.Fatal("unrelated origin tried to load missing credential")
	}
	if other.Header.Get("Authorization") != "" {
		t.Fatal("credential forwarded")
	}
}
