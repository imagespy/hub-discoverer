package registry

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrAuthTokenNoBearer indicates that a registry did not return the expected authenticaton header.
	ErrAuthTokenNoBearer = fmt.Errorf("Www-authenticate header value does not start with 'Bearer'")
)

// A Authenticator is responsible for authenticating against the registry.
type Authenticator interface {
	// HandleRequest is called each time before a request is sent to the registry.
	HandleRequest(r *http.Request) error
	// HandleResponse is called each time after a response is received from the registry.
	HandleResponse(resp *http.Response) (*http.Response, bool, error)
}

type basicAuthenticator struct {
	password string
	username string
}

func (b *basicAuthenticator) HandleRequest(r *http.Request) error {
	if b.username != "" {
		r.SetBasicAuth(b.username, b.password)
	}

	return nil
}

func (b *basicAuthenticator) HandleResponse(resp *http.Response) (*http.Response, bool, error) {
	return resp, false, nil
}

// NewBasicAuthenticator returns an Authenticator that handles basic authentication.
func NewBasicAuthenticator(username, password string) Authenticator {
	return &basicAuthenticator{password: password, username: username}
}

type nullAuthenticator struct{}

func (n *nullAuthenticator) HandleRequest(r *http.Request) error { return nil }

func (n *nullAuthenticator) HandleResponse(r *http.Response) (*http.Response, bool, error) {
	return r, false, nil
}

// NewNullAuthenticator returns an Authenticator that does not modify the request or the response.
// It is used as a fallback if not Authenticator is set.
func NewNullAuthenticator() Authenticator {
	return &nullAuthenticator{}
}

type tokenResponse struct {
	ExpiresIn int64
	Token     string
}

type tokenAuthenticator struct {
	client    *http.Client
	expiresAt time.Time
	realm     string
	scope     string
	service   string
	token     string
}

func (t *tokenAuthenticator) HandleRequest(r *http.Request) error {
	if t.token != "" {
		if t.expiresAt.Before(time.Now()) {
			err := t.requestToken()
			if err != nil {
				return err
			}
		}

		r.Header.Set("Authorization", "Bearer "+t.token)
	}

	return nil
}

func (t *tokenAuthenticator) HandleResponse(resp *http.Response) (*http.Response, bool, error) {
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, false, nil
	}

	wwwAuth := resp.Header.Get("www-authenticate")
	var parseErr error
	t.realm, t.scope, t.service, parseErr = parseAuthHeader(wwwAuth)
	if parseErr != nil {
		return nil, false, parseErr
	}

	err := t.requestToken()
	if err != nil {
		return nil, false, err
	}

	return resp, true, nil
}

func (t *tokenAuthenticator) requestToken() error {
	r, err := http.NewRequest("GET", t.realm, nil)
	if err != nil {
		return err
	}

	q := r.URL.Query()
	q.Set("scope", t.scope)
	q.Set("service", t.service)
	r.URL.RawQuery = q.Encode()
	resp, err := t.client.Do(r)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	tr := tokenResponse{}
	err = json.Unmarshal(data, &tr)
	if err != nil {
		return err
	}

	t.token = tr.Token
	t.expiresAt = time.Now().UTC().Add(time.Duration(tr.ExpiresIn - 30))
	return nil
}

// NewTokenAuthenticator returns an Authenticator that handles authentication as described in https://docs.docker.com/registry/spec/auth/.
func NewTokenAuthenticator() Authenticator {
	return &tokenAuthenticator{
		client: &http.Client{},
	}
}

func parseAuthHeader(h string) (string, string, string, error) {
	if !strings.HasPrefix(h, "Bearer ") {
		return "", "", "", ErrAuthTokenNoBearer
	}

	var realm string
	var scope string
	var service string
	parts := strings.Split(strings.TrimPrefix(h, "Bearer "), ",")
	for _, p := range parts {
		kv := strings.Split(p, "=")
		switch kv[0] {
		case "realm":
			realm = strings.Trim(kv[1], `"`)
		case "scope":
			scope = strings.Trim(kv[1], `"`)
		case "service":
			service = strings.Trim(kv[1], `"`)
		}
	}

	return realm, scope, service, nil
}
