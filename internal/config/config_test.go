package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMailDefaultsAndExplicitFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("database:\n  path: mail.db\nentra:\n  tenant_id: tenant\n  client_id: client\nmail:\n  include_received: false\n  include_sent: true\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mail.ReceivedEnabled() || !cfg.Mail.SentEnabled() {
		t.Fatalf("mail flags: received=%t sent=%t", cfg.Mail.ReceivedEnabled(), cfg.Mail.SentEnabled())
	}
	if cfg.Sync.MailInitialLookbackDays != 365 || len(cfg.Mail.Folders.Include) != 3 || !filepath.IsAbs(cfg.Database.Path) {
		t.Fatalf("cfg=%+v", cfg)
	}
	if len(cfg.Calendar.Calendars) != 1 || cfg.Calendar.Calendars[0].ID != "primary" || cfg.Calendar.Range.PastDays != 1095 || cfg.Calendar.DisplayTimezone != "Asia/Tokyo" {
		t.Fatalf("calendar defaults=%+v", cfg.Calendar)
	}
}
