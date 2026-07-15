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
		Interval                time.Duration `yaml:"interval"`
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
type Team struct {
	ID       string `yaml:"id"`
	Enabled  bool   `yaml:"enabled"`
	Channels struct {
		IncludeAll bool     `yaml:"include_all"`
		ExcludeIDs []string `yaml:"exclude_ids"`
	} `yaml:"channels"`
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
type Calendar struct {
	DisplayTimezone string `yaml:"display_timezone"`
	Range           struct {
		PastDays   int `yaml:"past_days"`
		FutureDays int `yaml:"future_days"`
	} `yaml:"range"`
	Calendars     []CalendarSelection `yaml:"calendars"`
	PrivateEvents struct {
		StoreDetails bool `yaml:"store_details"`
		ExposeToMCP  bool `yaml:"expose_to_mcp"`
	} `yaml:"private_events"`
	SyncWindows struct {
		RecentMonthsPerWindow     int `yaml:"recent_months_per_window"`
		HistoricalMonthsPerWindow int `yaml:"historical_months_per_window"`
		FutureMonthsPerWindow     int `yaml:"future_months_per_window"`
	} `yaml:"sync_windows"`
}
type CalendarSelection struct {
	ID      string `yaml:"id"`
	Enabled bool   `yaml:"enabled"`
}

var defaultMailHeaders = []string{"To", "Cc", "Delivered-To", "X-Original-To", "Envelope-To", "X-Envelope-To"}

func parseFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	b = []byte(os.ExpandEnv(string(b)))
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
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
	return c, nil
}
func resolveDatabasePath(c *Config, configPath, fallback string) {
	if c.Database.Path == "" {
		c.Database.Path = fallback
	}
	if !filepath.IsAbs(c.Database.Path) {
		c.Database.Path = filepath.Join(filepath.Dir(configPath), c.Database.Path)
	}
	c.Database.Path = filepath.Clean(c.Database.Path)
}
func Load(path string) (Config, error) {
	c, err := parseFile(path)
	if err != nil {
		return c, err
	}
	resolveDatabasePath(&c, path, "./data/teams-knowledge.db")
	if c.Sync.InitialLookbackDays == 0 {
		c.Sync.InitialLookbackDays = 365
	}
	if c.Sync.OverlapDuration == 0 {
		c.Sync.OverlapDuration = 24 * time.Hour
	}
	if c.Notifications.ListenAddress == "" {
		c.Notifications.ListenAddress = "127.0.0.1:8787"
	}
	return c, c.Validate()
}
func LoadOutlook(path string) (Config, error) {
	c, err := parseFile(path)
	if err != nil {
		return c, err
	}
	resolveDatabasePath(&c, path, "./data/outlook-knowledge.db")
	if c.Sync.Interval == 0 {
		c.Sync.Interval = 5 * time.Minute
	}
	if c.Sync.MailInitialLookbackDays == 0 {
		c.Sync.MailInitialLookbackDays = 365
	}
	if c.Mail.IncludeReceived == nil {
		c.Mail.IncludeReceived = boolPtr(true)
	}
	if c.Mail.IncludeSent == nil {
		c.Mail.IncludeSent = boolPtr(true)
	}
	if len(c.Mail.Folders.Include) == 0 {
		c.Mail.Folders.Include = []string{"inbox", "sentitems", "archive"}
	}
	if len(c.Mail.Folders.Exclude) == 0 {
		c.Mail.Folders.Exclude = []string{"deleteditems", "junkemail", "drafts", "outbox"}
	}
	for i := range c.Mail.Addresses {
		if c.Mail.Addresses[i].Enabled == nil {
			c.Mail.Addresses[i].Enabled = boolPtr(true)
		}
		if len(c.Mail.Addresses[i].Match.Headers) == 0 {
			c.Mail.Addresses[i].Match.Headers = defaultMailHeaders
		}
	}
	if c.Calendar.DisplayTimezone == "" {
		c.Calendar.DisplayTimezone = "Asia/Tokyo"
	}
	if c.Calendar.Range.PastDays == 0 {
		c.Calendar.Range.PastDays = 1095
	}
	if c.Calendar.Range.FutureDays == 0 {
		c.Calendar.Range.FutureDays = 365
	}
	if len(c.Calendar.Calendars) == 0 {
		c.Calendar.Calendars = []CalendarSelection{{ID: "primary", Enabled: true}}
	}
	if c.Calendar.SyncWindows.RecentMonthsPerWindow == 0 {
		c.Calendar.SyncWindows.RecentMonthsPerWindow = 1
	}
	if c.Calendar.SyncWindows.HistoricalMonthsPerWindow == 0 {
		c.Calendar.SyncWindows.HistoricalMonthsPerWindow = 3
	}
	if c.Calendar.SyncWindows.FutureMonthsPerWindow == 0 {
		c.Calendar.SyncWindows.FutureMonthsPerWindow = 3
	}
	return c, c.ValidateOutlook()
}
func boolPtr(v bool) *bool { return &v }
func (c Config) Validate() error {
	if err := c.validateEntra(); err != nil {
		return err
	}
	if c.Notifications.PublicURL == "" {
		return fmt.Errorf("notifications.public_url is required")
	}
	return nil
}
func (c Config) ValidateOutlook() error { return c.validateEntra() }
func (c Config) validateEntra() error {
	if c.Entra.TenantID == "" || strings.Contains(c.Entra.TenantID, "${") {
		return fmt.Errorf("entra.tenant_id is required")
	}
	if c.Entra.ClientID == "" || strings.Contains(c.Entra.ClientID, "${") {
		return fmt.Errorf("entra.client_id is required")
	}
	return nil
}
