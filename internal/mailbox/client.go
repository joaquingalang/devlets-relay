// Package mailbox is the CLI's HTTP client for the relay mailbox API.
// It makes one attempt per call (10 s timeout, no retries) and never puts a
// token or key in an error message.
package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/crypto"
)

const timeout = 10 * time.Second

// ErrGone means the relay answered 404 {"error":"not_found"}: the box is gone
// or the token is wrong. A 404 with any other body (a proxy, a wrong base URL)
// is a StatusError, never ErrGone.
var ErrGone = errors.New("mailbox: not found")

// goneBody is the relay's exact 404 body.
const goneBody = `{"error":"not_found"}`

// RateLimited is returned for a 429.
type RateLimited struct{ RetryAfter time.Duration }

func (e *RateLimited) Error() string {
	return fmt.Sprintf("mailbox: rate limited, retry after %s", e.RetryAfter)
}

// StatusError is any other unexpected status.
type StatusError struct{ Status int }

func (e *StatusError) Error() string { return fmt.Sprintf("mailbox: unexpected status %d", e.Status) }

// Client talks to the relay API at Base.
type Client struct {
	Base    string       // e.g. https://api.getdevlets.com
	HTTP    *http.Client // nil means a plain client; every request still has a 10 s timeout
	Version string       // CLI version, for the User-Agent
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

func (c *Client) do(ctx context.Context, method, path, token string, body any) (*http.Response, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return nil, nil, errors.New("mailbox: bad request")
	}
	req.Header.Set("User-Agent", fmt.Sprintf("devlets/%s (%s/%s)", c.Version, runtime.GOOS, runtime.GOARCH))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// url.Error carries the URL (which holds the box id); report only the kind.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, errors.New("mailbox: request timed out")
		}
		return nil, nil, errors.New("mailbox: request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, nil, errors.New("mailbox: reading response failed")
	}
	return resp, data, nil
}

func statusErr(resp *http.Response, body []byte) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		if strings.TrimSpace(string(body)) == goneBody {
			return ErrGone
		}
	case http.StatusTooManyRequests:
		s, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &RateLimited{RetryAfter: time.Duration(s) * time.Second}
	}
	return &StatusError{Status: resp.StatusCode}
}

// Create opens a box and returns its id and write token.
func (c *Client) Create(ctx context.Context, readTokenHash string) (boxID, writeToken string, err error) {
	resp, data, err := c.do(ctx, http.MethodPost, "/v1/relay/boxes", "", map[string]string{"readTokenHash": readTokenHash})
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", statusErr(resp, data)
	}
	var out struct {
		BoxID      string `json:"boxId"`
		WriteToken string `json:"writeToken"`
	}
	if json.Unmarshal(data, &out) != nil || out.BoxID == "" || out.WriteToken == "" {
		return "", "", errors.New("mailbox: malformed create response")
	}
	return out.BoxID, out.WriteToken, nil
}

// Put stores the newest reading.
func (c *Client) Put(ctx context.Context, boxID, writeToken string, env crypto.Envelope) error {
	resp, data, err := c.do(ctx, http.MethodPut, "/v1/relay/boxes/"+boxID, writeToken, env)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return statusErr(resp, data)
	}
	return nil
}

// Hello fetches the phone's hello, or nil when there is none yet (204).
func (c *Client) Hello(ctx context.Context, boxID, writeToken string) (*crypto.Envelope, error) {
	resp, data, err := c.do(ctx, http.MethodGet, "/v1/relay/boxes/"+boxID+"/hello", writeToken, nil)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var env crypto.Envelope
		if json.Unmarshal(data, &env) != nil {
			return nil, errors.New("mailbox: malformed hello response")
		}
		return &env, nil
	}
	return nil, statusErr(resp, data)
}

// Delete removes the box; token may be either token.
func (c *Client) Delete(ctx context.Context, boxID, token string) error {
	resp, data, err := c.do(ctx, http.MethodDelete, "/v1/relay/boxes/"+boxID, token, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return statusErr(resp, data)
	}
	return nil
}
