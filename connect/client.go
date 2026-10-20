package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const callTimeout = 30 * time.Second

// session is an administrator signed in to one tool's API by password.
// It keeps the cookies itself rather than in a cookiejar, which would drop a
// Secure cookie over the plain http of the in-network address.
type session struct {
	product string
	base    string
	client  *http.Client
	cookies map[string]string
}

func newSession(product, api string) *session {
	return &session{
		product: product,
		base:    strings.TrimRight(api, "/") + "/api/v1",
		client:  &http.Client{Timeout: callTimeout},
		cookies: map[string]string{},
	}
}

// refusal is an answer of 400 or more, with the tool's own words.
type refusal struct {
	product, method, path string
	status                int
	body                  string
}

func (r *refusal) Error() string {
	return fmt.Sprintf("%s answered %s %s with %d: %s", r.product, r.method, r.path, r.status, r.body)
}

func hasStatus(err error, status int) bool {
	var r *refusal
	return errors.As(err, &r) && r.status == status
}

func (s *session) send(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for name, value := range s.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s did not answer %s %s: %w", s.product, method, path, err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 {
			delete(s.cookies, c.Name)
		} else {
			s.cookies[c.Name] = c.Value
		}
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		return &refusal{product: s.product, method: method, path: path, status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s answered %s %s with something that is not the expected JSON: %w", s.product, method, path, err)
		}
	}
	return nil
}

func (s *session) signIn(ctx context.Context, email, password string) error {
	err := s.send(ctx, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password}, nil)
	if err != nil {
		return fmt.Errorf("sign in to %s as %s: %w", s.product, email, err)
	}
	return nil
}
