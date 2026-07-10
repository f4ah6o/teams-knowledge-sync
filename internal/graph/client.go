package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/obr-grp/teams-knowledge-sync/internal/auth"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	auth    *auth.Manager
	http    *http.Client
	retries int
}

func New(a *auth.Manager, timeout time.Duration, retries int) *Client {
	return &Client{a, &http.Client{Timeout: timeout}, retries}
}
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	for i := 0; i <= c.retries; i++ {
		token, e := c.auth.AccessToken(ctx)
		if e != nil {
			return e
		}
		target := "https://graph.microsoft.com/v1.0/" + strings.TrimPrefix(path, "/")
		if strings.HasPrefix(path, "https://graph.microsoft.com/") {
			target = path
		}
		if strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "https://graph.microsoft.com/") {
			return fmt.Errorf("unsupported Graph URL host")
		}
		req, e := http.NewRequestWithContext(ctx, method, target, strings.NewReader(string(raw)))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
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
		if graphError.Error.Code != "" {
			return fmt.Errorf("graph %s %s: %s (%s: %s)", method, path, res.Status, graphError.Error.Code, graphError.Error.Message)
		}
		return fmt.Errorf("graph %s %s: %s", method, path, res.Status)
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
			path = strings.TrimPrefix(path, "https://graph.microsoft.com/v1.0/")
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
