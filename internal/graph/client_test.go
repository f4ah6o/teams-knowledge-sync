package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type staticToken struct{}

func (staticToken) AccessToken(context.Context) (string, error) { return "test-token", nil }

func testClient(srv *httptest.Server, retries int) *Client {
	return &Client{auth: staticToken{}, http: srv.Client(), retries: retries}
}

func TestGetPageParsesLinksAndUsesAbsoluteURLVerbatim(t *testing.T) {
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		if r.Header.Get("Prefer") != `IdType="ImmutableId"` {
			t.Errorf("Prefer header missing: %q", r.Header.Get("Prefer"))
		}
		if r.URL.RawQuery == "$deltatoken=abc" {
			w.Write([]byte(`{"value":[{"id":"m2"}],"@odata.deltaLink":"` + serverURL(r) + `/delta?$deltatoken=def"}`))
			return
		}
		w.Write([]byte(`{"value":[{"id":"m1"}],"@odata.nextLink":"` + serverURL(r) + `/delta?$deltatoken=abc"}`))
	}))
	defer srv.Close()
	c := testClient(srv, 0)
	h := map[string]string{"Prefer": `IdType="ImmutableId"`}
	p, err := c.GetPage(context.Background(), srv.URL+"/first", h)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Value) != 1 || p.NextLink == "" || p.DeltaLink != "" {
		t.Fatalf("page=%+v", p)
	}
	p, err = c.GetPage(context.Background(), p.NextLink, h)
	if err != nil {
		t.Fatal(err)
	}
	if p.DeltaLink == "" {
		t.Fatalf("page=%+v", p)
	}
	if got[1] != "/delta?$deltatoken=abc" {
		t.Fatalf("nextLink not used verbatim: %v", got)
	}
}

func serverURL(r *http.Request) string { return "https://" + r.Host }

func TestGetPageRetriesOn429WithRetryAfter(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"value":[]}`))
	}))
	defer srv.Close()
	c := testClient(srv, 2)
	start := time.Now()
	if _, err := c.GetPage(context.Background(), srv.URL+"/x", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	if time.Since(start) < time.Second {
		t.Fatal("Retry-After not honored")
	}
}

func TestGetPage410ReturnsTypedError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"error":{"code":"SyncStateNotFound","message":"resync"}}`))
	}))
	defer srv.Close()
	c := testClient(srv, 0)
	_, err := c.GetPage(context.Background(), srv.URL+"/x", nil)
	if err == nil || !IsSyncStateInvalid(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestIsSyncStateInvalidByCode(t *testing.T) {
	if IsSyncStateInvalid(&Error{Status: 400, Code: "resyncRequired"}) != true {
		t.Fatal("resyncRequired not detected")
	}
	if IsSyncStateInvalid(&Error{Status: 403, Code: "Forbidden"}) {
		t.Fatal("403 misdetected")
	}
}
