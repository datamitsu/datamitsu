package httpx

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/datamitsu/datamitsu/internal/env"
)

// RequestAuth stores a credential reference, never the credential value.
type RequestAuth struct {
	TokenEnv string `json:"tokenEnv"`
	Origin   string `json:"origin"`
	Header   string `json:"header"`
	Scheme   string `json:"scheme,omitempty"`
	Accept   string `json:"accept,omitempty"`
}

// Validate restricts credentials to explicit origins and recognized token headers.
func (a *RequestAuth) Validate() error {
	if a == nil {
		return nil
	}
	if _, err := env.Credential(a.TokenEnv); err != nil {
		return err
	}
	u, err := url.Parse(a.Origin)
	if err != nil {
		return errors.New("malformed auth origin")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("auth origin must contain only scheme and host")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return errors.New("auth origin requires HTTPS or loopback HTTP")
	}
	if a.Header != "Authorization" && a.Header != "PRIVATE-TOKEN" {
		return errors.New("auth header must be Authorization or PRIVATE-TOKEN")
	}
	if a.Header == "Authorization" && a.Scheme != "Bearer" && a.Scheme != "token" {
		return errors.New("authorization requires Bearer or token scheme")
	}
	if a.Header == "PRIVATE-TOKEN" && a.Scheme != "" {
		return errors.New("private-token does not take an auth scheme")
	}
	if a.Accept != "" && a.Accept != "application/octet-stream" {
		return errors.New("unsupported authenticated download Accept header")
	}
	return nil
}

// SameOrigin compares HTTP origins with case-insensitive hosts and explicit default ports.
func SameOrigin(a, b string) bool {
	x, xe := url.Parse(a)
	y, ye := url.Parse(b)
	if xe != nil || ye != nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		if strings.EqualFold(u.Scheme, "http") {
			return "80"
		}
		return ""
	}
	return strings.EqualFold(x.Scheme, y.Scheme) && strings.EqualFold(x.Hostname(), y.Hostname()) && port(x) == port(y)
}

// Apply attaches a credential only to the exact configured origin.
func (a *RequestAuth) Apply(req *http.Request) error {
	if a == nil {
		return nil
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if !SameOrigin(req.URL.String(), a.Origin) {
		return nil
	}
	token, err := env.Credential(a.TokenEnv)
	if err != nil {
		return err
	}
	if token != "" {
		if a.Scheme != "" {
			token = a.Scheme + " " + token
		}
		req.Header.Set(a.Header, token)
	}
	if a.Accept != "" {
		req.Header.Set("Accept", a.Accept)
	}
	return nil
}

// WithAuth returns a client copy that never forwards token headers across origins.
func WithAuth(client *http.Client, auth *RequestAuth) (*http.Client, error) {
	if err := auth.Validate(); err != nil {
		return nil, err
	}
	copyClient := *client
	original := client.CheckRedirect
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if original != nil {
			if err := original(req, via); err != nil {
				return err
			}
		}
		req.Header.Del("Authorization")
		req.Header.Del("Private-Token")
		return auth.Apply(req)
	}
	return &copyClient, nil
}
