package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database struct {
		Path string `yaml:"path"`
	} `yaml:"database"`
	Entra struct {
		TenantID string `yaml:"tenant_id"`
		ClientID string `yaml:"client_id"`
	} `yaml:"entra"`
	Sync struct {
		InitialLookbackDays     int           `yaml:"initial_lookback_days"`
		MailInitialLookbackDays int           `yaml:"mail_initial_lookback_days"`
		OverlapDuration         time.Duration `yaml:"overlap_duration"`
		FullResyncInterval      time.Duration `yaml:"full_resync_interval"`
		RequestTimeout          time.Duration `yaml:"request_timeout"`
		MaxRetries              int           `yaml:"max_retries"`
	} `yaml:"sync"`
	Notifications struct {
		ListenAddress string `yaml:"listen_address"`
		PublicURL     string `yaml:"public_url"`
	} `yaml:"notifications"`
	Teams []Team `yaml:"teams"`
	Chats struct {
		IncludeMyChats  bool     `yaml:"include_my_chats"`
		IncludeOneOnOne bool     `yaml:"include_one_on_one"`
		IncludeGroup    bool     `yaml:"include_group"`
		IncludeMeeting  bool     `yaml:"include_meeting"`
		ExcludeIDs      []string `yaml:"exclude_ids"`
	} `yaml:"chats"`
	Mail     Mail     `yaml:"mail"`
	Calendar Calendar `yaml:"calendar"`
}

type Calendar struct {
	Calendars []CalendarSelection `yaml:"calendars"`
	Range     struct {
		PastDays   int `yaml:"past_days"`
		FutureDays int `yaml:"future_days"`
	} `yaml:"range"`
	DisplayTimezone string `yaml:"display_timezone"`
	PrivateEvents   struct {
		StoreDetails *bool `yaml:"store_details"`
		ExposeToMCP  *bool `yaml:"expose_to_mcp"`
	} `yaml:"private_events"`
}
type CalendarSelection struct {
	ID      string `yaml:"id"`
	Enabled *bool  `yaml:"enabled"`
}

func (c CalendarSelection) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }
func (c Calendar) StorePrivateDetails() bool {
	return c.PrivateEvents.StoreDetails != nil && *c.PrivateEvents.StoreDetails
}

type Mail struct {
	IncludeReceived *bool         `yaml:"include_received"`
	IncludeSent     *bool         `yaml:"include_sent"`
	Addresses       []MailAddress `yaml:"addresses"`
	Folders         struct {
		Include []string `yaml:"include"`
		Exclude []string `yaml:"exclude"`
	} `yaml:"folders"`
}

type MailAddress struct {
	Address string `yaml:"address"`
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"`
	Match   struct {
		Headers         []string `yaml:"headers"`
		SubjectPrefixes []string `yaml:"subject_prefixes"`
	} `yaml:"match"`
}

func (m Mail) ReceivedEnabled() bool  { return m.IncludeReceived == nil || *m.IncludeReceived }
func (m Mail) SentEnabled() bool      { return m.IncludeSent == nil || *m.IncludeSent }
func (a MailAddress) IsEnabled() bool { return a.Enabled == nil || *a.Enabled }

type Team struct {
	ID       string `yaml:"id"`
	Enabled  bool   `yaml:"enabled"`
	Channels struct {
		IncludeAll bool     `yaml:"include_all"`
		ExcludeIDs []string `yaml:"exclude_ids"`
	} `yaml:"channels"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	b = []byte(os.ExpandEnv(string(b)))
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Database.Path == "" {
		c.Database.Path = "./data/teams-knowledge.db"
	}
	if !filepath.IsAbs(c.Database.Path) {
		c.Database.Path = filepath.Join(filepath.Dir(path), c.Database.Path)
	}
	c.Database.Path = filepath.Clean(c.Database.Path)
	if c.Sync.InitialLookbackDays == 0 {
		c.Sync.InitialLookbackDays = 365
	}
	if c.Sync.MailInitialLookbackDays == 0 {
		c.Sync.MailInitialLookbackDays = 365
	}
	if len(c.Mail.Folders.Include) == 0 {
		c.Mail.Folders.Include = []string{"inbox", "sentitems", "archive"}
	}
	if len(c.Mail.Folders.Exclude) == 0 {
		c.Mail.Folders.Exclude = []string{"deleteditems", "junkemail", "drafts", "outbox"}
	}
	if len(c.Calendar.Calendars) == 0 {
		c.Calendar.Calendars = []CalendarSelection{{ID: "primary"}}
	}
	if c.Calendar.Range.PastDays == 0 {
		c.Calendar.Range.PastDays = 1095
	}
	if c.Calendar.Range.FutureDays == 0 {
		c.Calendar.Range.FutureDays = 365
	}
	if c.Calendar.DisplayTimezone == "" {
		c.Calendar.DisplayTimezone = "Asia/Tokyo"
	}
	if c.Sync.OverlapDuration == 0 {
		c.Sync.OverlapDuration = 24 * time.Hour
	}
	if c.Sync.FullResyncInterval == 0 {
		c.Sync.FullResyncInterval = 24 * time.Hour
	}
	if c.Sync.RequestTimeout == 0 {
		c.Sync.RequestTimeout = 30 * time.Second
	}
	if c.Sync.MaxRetries == 0 {
		c.Sync.MaxRetries = 5
	}
	if c.Notifications.ListenAddress == "" {
		c.Notifications.ListenAddress = "127.0.0.1:8787"
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Entra.TenantID == "" || strings.Contains(c.Entra.TenantID, "${") {
		return fmt.Errorf("entra.tenant_id is required")
	}
	if c.Entra.ClientID == "" || strings.Contains(c.Entra.ClientID, "${") {
		return fmt.Errorf("entra.client_id is required")
	}
	return nil
}
