package liverelay

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Authorizer interface {
	Authorize(ctx context.Context, token string, library string) error
}

type HTTPAuthorizer struct {
	base *url.URL
	http *http.Client
}

type staticAuthorizer struct {
	token string
}

func NewHTTPAuthorizer(authorityURL string) (*HTTPAuthorizer, error) {
	base, err := url.Parse(authorityURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("live relay: invalid authority URL")
	}
	return &HTTPAuthorizer{
		base: base,
		http: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (a *HTTPAuthorizer) Authorize(ctx context.Context, token string, library string) error {
	if token == "" {
		return fmt.Errorf("live relay: bearer token required")
	}
	endpoint := *a.base
	endpoint.Path = strings.TrimRight(a.base.Path, "/") + "/v1/libraries/" + url.PathEscape(library) + "/authorize"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := a.http.Do(request)
	if err != nil {
		return fmt.Errorf("live relay: authorize request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("live relay: authorization rejected")
	}
	return nil
}

func (a staticAuthorizer) Authorize(ctx context.Context, token string, library string) error {
	_ = ctx
	_ = library
	if len(token) != len(a.token) {
		return fmt.Errorf("live relay: authorization rejected")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
		return fmt.Errorf("live relay: authorization rejected")
	}
	return nil
}
