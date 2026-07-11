package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const base = "https://graph.microsoft.com/v1.0/"

type tokenSource interface {
	AccessToken(context.Context) (string, error)
}
type Client struct {
	auth    tokenSource
	http    *http.Client
	retries int
}

type Error struct {
	Status                  int
	Method, URL, StatusText string
	Code, Message           string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("graph %s %s: %s (%s: %s)", e.Method, e.URL, e.StatusText, e.Code, e.Message)
	}
	return fmt.Sprintf("graph %s %s: %s", e.Method, e.URL, e.StatusText)
}

func IsSyncStateInvalid(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	if ge.Status == http.StatusGone {
		return true
	}
	switch ge.Code {
	case "SyncStateNotFound", "SyncStateInvalid", "resyncRequired":
		return true
	}
	return false
}

type PageResult struct {
	Value     []json.RawMessage `json:"value"`
	NextLink  string            `json:"@odata.nextLink"`
	DeltaLink string            `json:"@odata.deltaLink"`
}

func New(a *auth.Manager, timeout time.Duration, retries int) *Client {
	return &Client{a, &http.Client{Timeout: timeout}, retries}
}
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	return c.do(ctx, method, base+strings.TrimPrefix(path, "/"), nil, body, out)
}

// GetPage fetches a single page. pageURL may be a relative v1.0 path or an
// absolute URL (stored nextLink/deltaLink), which is requested verbatim.
func (c *Client) GetPage(ctx context.Context, pageURL string, headers map[string]string) (PageResult, error) {
	var p PageResult
	u := pageURL
	if !strings.HasPrefix(u, "https://") {
		u = base + strings.TrimPrefix(u, "/")
	}
	return p, c.do(ctx, http.MethodGet, u, headers, nil, &p)
}
func (c *Client) do(ctx context.Context, method, fullURL string, headers map[string]string, body any, out any) error {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	for i := 0; i <= c.retries; i++ {
		token, e := c.auth.AccessToken(ctx)
		if e != nil {
			return e
		}
		req, e := http.NewRequestWithContext(ctx, method, fullURL, strings.NewReader(string(raw)))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, e := c.http.Do(req)
		if e != nil {
			return e
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		if res.StatusCode/100 == 2 {
			if out != nil && len(b) > 0 {
				return json.Unmarshal(b, out)
			}
			return nil
		}
		if res.StatusCode == 429 || res.StatusCode >= 500 {
			wait := time.Duration(i+1) * time.Second
			if v, _ := strconv.Atoi(res.Header.Get("Retry-After")); v > 0 {
				wait = time.Duration(v) * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		var graphError struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &graphError)
		return &Error{Status: res.StatusCode, Method: method, URL: fullURL, StatusText: res.Status, Code: graphError.Error.Code, Message: graphError.Error.Message}
	}
	return fmt.Errorf("graph retry limit exceeded")
}
func (c *Client) Page(ctx context.Context, path string, fn func(json.RawMessage) error) error {
	return c.PageUntil(ctx, path, func(raw json.RawMessage) (bool, error) {
		return true, fn(raw)
	})
}

func (c *Client) PageUntil(ctx context.Context, path string, fn func(json.RawMessage) (bool, error)) error {
	for path != "" {
		var p struct {
			Value []json.RawMessage `json:"value"`
			Next  string            `json:"@odata.nextLink"`
		}
		if strings.HasPrefix(path, "https://") {
			path = strings.TrimPrefix(path, base)
		}
		if e := c.Do(ctx, http.MethodGet, path, nil, &p); e != nil {
			return e
		}
		for _, v := range p.Value {
			cont, e := fn(v)
			if e != nil {
				return e
			}
			if !cont {
				return nil
			}
		}
		path = p.Next
	}
	return nil
}
func Escape(s string) string { return url.PathEscape(s) }
