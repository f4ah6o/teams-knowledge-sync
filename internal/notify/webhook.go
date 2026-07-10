package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	"io"
	"net/http"
	"strings"
)

type Server struct {
	Store  *store.Store
	Secret []byte
}

func (s Server) State(resource string) string {
	h := hmac.New(sha256.New, s.Secret)
	h.Write([]byte(resource))
	return hex.EncodeToString(h.Sum(nil))
}
func (s Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		if v := r.URL.Query().Get("validationToken"); v != "" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(v))
			return
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if e != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var n struct {
			Value []struct {
				ClientState string `json:"clientState"`
				Resource    string `json:"resource"`
			} `json:"value"`
		}
		if json.Unmarshal(b, &n) != nil || len(n.Value) == 0 {
			http.Error(w, "bad notification", 400)
			return
		}
		for _, v := range n.Value {
			if !hmac.Equal([]byte(v.ClientState), []byte(s.State(v.Resource))) {
				http.Error(w, "invalid client state", 401)
				return
			}
		}
		if e = s.Store.QueueNotification(context.Background(), b); e != nil {
			http.Error(w, "queue unavailable", 503)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
func Path(publicURL string) string {
	if i := strings.Index(publicURL, "//"); i >= 0 {
		if j := strings.Index(publicURL[i+2:], "/"); j >= 0 {
			return publicURL[i+2+j:]
		}
	}
	return "/graph/notifications"
}
