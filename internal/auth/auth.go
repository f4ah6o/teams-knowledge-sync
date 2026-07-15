package auth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}
type Manager struct {
	tenant, client, cachePath, service, scope string
	http                                      *http.Client
}

func New(tenant, client string) *Manager {
	return NewFor(tenant, client, "teams-knowledge-sync", []string{"offline_access", "User.Read", "Team.ReadBasic.All", "Channel.ReadBasic.All", "ChannelMessage.Read.All", "Chat.Read"})
}
func NewFor(tenant, client, app string, scopes []string) *Manager {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return &Manager{tenant: tenant, client: client, cachePath: filepath.Join(base, app, "token.cache"), service: app, scope: strings.Join(scopes, " "), http: &http.Client{Timeout: 30 * time.Second}}
}
func (m *Manager) tokenURL() string {
	return "https://login.microsoftonline.com/" + url.PathEscape(m.tenant) + "/oauth2/v2.0/token"
}
func (m *Manager) Load() (Token, error) {
	key, e := m.key()
	if e != nil {
		return Token{}, e
	}
	b, e := os.ReadFile(m.cachePath)
	if e != nil {
		return Token{}, e
	}
	raw, e := decrypt(key, b)
	if e != nil {
		return Token{}, fmt.Errorf("decrypt token cache: %w", e)
	}
	var t Token
	e = json.Unmarshal(raw, &t)
	return t, e
}
func (m *Manager) Save(t Token) error {
	key, e := m.key()
	if e != nil {
		return e
	}
	raw, e := json.Marshal(t)
	if e != nil {
		return e
	}
	blob, e := encrypt(key, raw)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(m.cachePath), 0700); e != nil {
		return e
	}
	return os.WriteFile(m.cachePath, blob, 0600)
}
func (m *Manager) Logout() error {
	_ = keyring.Delete(m.service, "cache-key:"+m.client)
	if e := os.Remove(m.cachePath); e != nil && !os.IsNotExist(e) {
		return e
	}
	return nil
}
func (m *Manager) key() ([]byte, error) {
	v, e := keyring.Get(m.service, "cache-key:"+m.client)
	if e == nil {
		return base64.StdEncoding.DecodeString(v)
	}
	if e != keyring.ErrNotFound {
		return nil, fmt.Errorf("OS credential store unavailable: %w", e)
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	if e = keyring.Set(m.service, "cache-key:"+m.client, base64.StdEncoding.EncodeToString(b)); e != nil {
		return nil, fmt.Errorf("save encryption key in OS credential store: %w", e)
	}
	return b, nil
}
func encrypt(key, raw []byte) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return g.Seal(n, n, raw, nil), nil
}
func decrypt(key, raw []byte) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(raw) < g.NonceSize() {
		return nil, fmt.Errorf("invalid cache")
	}
	return g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
}
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	t, e := m.Load()
	if e != nil {
		return "", e
	}
	if time.Until(t.ExpiresAt) > 2*time.Minute {
		return t.AccessToken, nil
	}
	if t.RefreshToken == "" {
		return "", fmt.Errorf("reauthentication required")
	}
	n, e := m.exchange(ctx, url.Values{"client_id": {m.client}, "grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}, "scope": {m.scope}})
	if e != nil {
		return "", e
	}
	if n.RefreshToken == "" {
		n.RefreshToken = t.RefreshToken
	}
	if e = m.Save(n); e != nil {
		return "", e
	}
	return n.AccessToken, nil
}
func (m *Manager) Login(ctx context.Context, notify func(string)) error {
	form := url.Values{"client_id": {m.client}, "scope": {m.scope}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/"+url.PathEscape(m.tenant)+"/oauth2/v2.0/devicecode", strings.NewReader(form.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := m.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	var d struct {
		DeviceCode string `json:"device_code"`
		Message    string `json:"message"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if e = json.NewDecoder(res.Body).Decode(&d); e != nil {
		return e
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("device code: %s", d.Message)
	}
	notify(d.Message)
	if d.Interval == 0 {
		d.Interval = 5
	}
	deadline := time.Now().Add(time.Duration(d.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(d.Interval) * time.Second):
		}
		t, e := m.exchange(ctx, url.Values{"client_id": {m.client}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {d.DeviceCode}})
		if e != nil {
			if strings.Contains(e.Error(), "authorization_pending") {
				continue
			}
			if strings.Contains(e.Error(), "slow_down") {
				d.Interval += 5
				continue
			}
			return e
		}
		return m.Save(t)
	}
	return fmt.Errorf("device code expired")
}
func (m *Manager) exchange(ctx context.Context, form url.Values) (Token, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL(), strings.NewReader(form.Encode()))
	if e != nil {
		return Token{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := m.http.Do(req)
	if e != nil {
		return Token{}, e
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var v struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &v)
	if res.StatusCode/100 != 2 {
		return Token{}, fmt.Errorf("token request %s: %s %s", res.Status, v.Error, v.Description)
	}
	return Token{AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(v.ExpiresIn) * time.Second)}, nil
}

var _ = bytes.MinRead
