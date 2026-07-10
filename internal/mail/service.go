package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/config"
	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/store"
	textutil "github.com/obr-grp/teams-knowledge-sync/internal/text"
)

type GraphAPI interface {
	Do(context.Context, string, string, any, any) error
	Page(context.Context, string, func(json.RawMessage) error) error
}

type Service struct {
	Graph  GraphAPI
	Store  *store.Store
	Config config.Config
	Now    func() time.Time
}

func (s Service) RegisterAddresses(ctx context.Context) error {
	var addresses []domain.MailAddress
	for _, a := range s.Config.Mail.Addresses {
		if a.IsEnabled() && NormalizeAddress(a.Address) != "" {
			addresses = append(addresses, domain.MailAddress{Address: NormalizeAddress(a.Address), Name: a.Name})
		}
	}
	return s.Store.ReplaceMailAddresses(ctx, addresses)
}

func (s Service) DiscoverFolders(ctx context.Context) ([]domain.MailFolder, error) {
	var folders []domain.MailFolder
	err := s.Graph.Page(ctx, "me/mailFolders?$top=100&includeHiddenFolders=true", func(raw json.RawMessage) error {
		f, err := parseFolder(raw)
		if err != nil {
			return err
		}
		f.Enabled = s.folderEnabled(f.ID, f.DisplayName, f.WellKnownName)
		if err = s.Store.UpsertMailFolder(ctx, f); err != nil {
			return err
		}
		folders = append(folders, f)
		return nil
	})
	return folders, err
}

func (s Service) Sync(ctx context.Context, onlyFolder string) error {
	if err := s.RegisterAddresses(ctx); err != nil {
		return err
	}
	folders, err := s.configuredFolders(ctx, onlyFolder)
	var failures []error
	if err != nil {
		failures = append(failures, err)
	}
	for _, folder := range folders {
		if err := s.syncFolder(ctx, folder); err != nil {
			failures = append(failures, fmt.Errorf("folder %s: %w", folder.DisplayName, err))
		}
	}
	return errors.Join(failures...)
}

func (s Service) configuredFolders(ctx context.Context, only string) ([]domain.MailFolder, error) {
	ids := s.Config.Mail.Folders.Include
	if only != "" {
		ids = []string{only}
	}
	var out []domain.MailFolder
	var failures []error
	seen := map[string]bool{}
	for _, id := range ids {
		if s.excluded(id) {
			continue
		}
		var raw json.RawMessage
		if err := s.Graph.Do(ctx, http.MethodGet, "me/mailFolders/"+graph.Escape(id), nil, &raw); err != nil {
			failures = append(failures, fmt.Errorf("resolve folder %s: %w", id, err))
			continue
		}
		folder, err := parseFolder(raw)
		if err != nil {
			return nil, err
		}
		if seen[folder.ID] {
			continue
		}
		seen[folder.ID] = true
		folder.WellKnownName, folder.Enabled = id, true
		if err = s.Store.UpsertMailFolder(ctx, folder); err != nil {
			return nil, err
		}
		out = append(out, folder)
	}
	return out, errors.Join(failures...)
}

func (s Service) syncFolder(ctx context.Context, folder domain.MailFolder) error {
	return s.syncFolderRound(ctx, folder, true)
}

type deltaPage struct {
	Value     []json.RawMessage `json:"value"`
	NextLink  string            `json:"@odata.nextLink"`
	DeltaLink string            `json:"@odata.deltaLink"`
}

func (s Service) syncFolderRound(ctx context.Context, folder domain.MailFolder, allowReset bool) error {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	started := now().UTC()
	since := started.Add(-time.Duration(s.Config.Sync.MailInitialLookbackDays) * 24 * time.Hour)
	selectFields := "id,internetMessageId,conversationId,conversationIndex,subject,body,bodyPreview,sender,from,toRecipients,ccRecipients,bccRecipients,replyTo,receivedDateTime,sentDateTime,createdDateTime,lastModifiedDateTime,isRead,isDraft,importance,flag,categories,hasAttachments,webLink,internetMessageHeaders"
	state, err := s.Store.MailSyncState(ctx, folder.ID)
	if err != nil {
		return err
	}
	path := state.NextLink
	if path == "" {
		path = state.DeltaLink
	}
	if path == "" {
		path = "me/mailFolders/" + graph.Escape(folder.ID) + "/messages/delta?$top=50&$orderby=receivedDateTime%20desc&$filter=" + url.QueryEscape("receivedDateTime ge "+since.Format(time.RFC3339)) + "&$select=" + selectFields + "&$expand=attachments($select=id,name,contentType,size,isInline,contentId)"
	}
	for path != "" {
		var page deltaPage
		if err = s.Graph.Do(ctx, http.MethodGet, path, nil, &page); err != nil {
			if allowReset && invalidDeltaToken(err) {
				if resetErr := s.Store.ResetMailSyncToken(ctx, folder.ID); resetErr != nil {
					return errors.Join(err, resetErr)
				}
				return s.syncFolderRound(ctx, folder, false)
			}
			return s.recordFolderFailure(ctx, folder.ID, started, err)
		}
		for _, raw := range page.Value {
			var marker struct {
				ID      string          `json:"id"`
				Removed json.RawMessage `json:"@removed"`
			}
			if err = json.Unmarshal(raw, &marker); err != nil {
				return s.recordFolderFailure(ctx, folder.ID, started, err)
			}
			if len(marker.Removed) > 0 {
				err = s.Store.TombstoneMailMessage(ctx, folder.ID, marker.ID, started)
			} else {
				var message domain.MailMessage
				message, err = parseMessage(raw, folder.ID)
				if err == nil {
					message.Matches = Classify(message, s.Config.Mail.Addresses, s.Config.Mail.ReceivedEnabled(), s.Config.Mail.SentEnabled())
					if len(message.Matches) == 0 {
						err = s.Store.TombstoneMailMessage(ctx, folder.ID, message.ID, started)
					} else {
						err = s.Store.UpsertMailMessage(ctx, message)
					}
				}
			}
			if err != nil {
				return s.recordFolderFailure(ctx, folder.ID, started, err)
			}
		}
		if page.NextLink != "" {
			if err = s.Store.RecordMailSyncProgress(ctx, folder.ID, page.NextLink, started); err != nil {
				return s.recordFolderFailure(ctx, folder.ID, started, err)
			}
			path = page.NextLink
			continue
		}
		if page.DeltaLink == "" {
			err = fmt.Errorf("delta response missing nextLink and deltaLink")
			return s.recordFolderFailure(ctx, folder.ID, started, err)
		}
		if err = s.Store.RecordMailSyncSuccess(ctx, folder.ID, page.DeltaLink, started); err != nil {
			return s.recordFolderFailure(ctx, folder.ID, started, err)
		}
		return nil
	}
	return nil
}

func (s Service) recordFolderFailure(ctx context.Context, folderID string, at time.Time, syncErr error) error {
	if err := s.Store.RecordMailSyncFailure(ctx, folderID, at, syncErr); err != nil {
		return errors.Join(syncErr, err)
	}
	return syncErr
}

func invalidDeltaToken(err error) bool {
	v := strings.ToLower(err.Error())
	return strings.Contains(v, "syncstatenotfound") || strings.Contains(v, "invaliddeltatoken") || strings.Contains(v, "410 gone")
}

func (s Service) Daemon(ctx context.Context) error {
	interval := s.Config.Sync.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	for {
		_ = s.Sync(ctx, "")
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s Service) excluded(id string) bool { return containsFold(s.Config.Mail.Folders.Exclude, id) }
func (s Service) folderEnabled(values ...string) bool {
	for _, v := range values {
		if s.excluded(v) {
			return false
		}
		if containsFold(s.Config.Mail.Folders.Include, v) {
			return true
		}
	}
	return false
}

func parseFolder(raw json.RawMessage) (domain.MailFolder, error) {
	var v struct {
		ID, ParentID, DisplayName, WellKnownName string
		ChildCount, TotalCount, UnreadCount      int
		Hidden                                   bool
	}
	var wire struct {
		ID            string `json:"id"`
		ParentID      string `json:"parentFolderId"`
		DisplayName   string `json:"displayName"`
		WellKnownName string `json:"wellKnownName"`
		ChildCount    int    `json:"childFolderCount"`
		TotalCount    int    `json:"totalItemCount"`
		UnreadCount   int    `json:"unreadItemCount"`
		Hidden        bool   `json:"isHidden"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return domain.MailFolder{}, err
	}
	v.ID, v.ParentID, v.DisplayName, v.WellKnownName, v.ChildCount, v.TotalCount, v.UnreadCount, v.Hidden = wire.ID, wire.ParentID, wire.DisplayName, wire.WellKnownName, wire.ChildCount, wire.TotalCount, wire.UnreadCount, wire.Hidden
	return domain.MailFolder{ID: v.ID, ParentID: v.ParentID, DisplayName: v.DisplayName, WellKnownName: v.WellKnownName, ChildCount: v.ChildCount, TotalCount: v.TotalCount, UnreadCount: v.UnreadCount, Hidden: v.Hidden}, nil
}

func parseMessage(raw json.RawMessage, folderID string) (domain.MailMessage, error) {
	type email struct{ Name, Address string }
	type party struct {
		Email email `json:"emailAddress"`
	}
	var v struct {
		ID                                string                                `json:"id"`
		InternetMessageID                 string                                `json:"internetMessageId"`
		ConversationID                    string                                `json:"conversationId"`
		ConversationIndex                 string                                `json:"conversationIndex"`
		Subject                           string                                `json:"subject"`
		Body                              struct{ ContentType, Content string } `json:"body"`
		BodyPreview                       string                                `json:"bodyPreview"`
		Sender, From                      party
		To, CC, BCC, ReplyTo              []party
		Received, Sent, Created, Modified string
		Read                              bool `json:"isRead"`
		Draft                             bool `json:"isDraft"`
		Importance                        string
		Flag                              struct{ Status string }
		Categories                        []string
		HasAttachments                    bool
		WebURL                            string                         `json:"webLink"`
		ETag                              string                         `json:"@odata.etag"`
		Headers                           []struct{ Name, Value string } `json:"internetMessageHeaders"`
		Attachments                       []struct {
			ID, Name, ContentType, ContentID string
			Size                             int
			Inline                           bool   `json:"isInline"`
			ODataType                        string `json:"@odata.type"`
		} `json:"attachments"`
	}
	// Graph field names that do not match Go field names are decoded through an alias map.
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return domain.MailMessage{}, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return domain.MailMessage{}, err
	}
	decode := func(key string, dst any) {
		if b := wire[key]; len(b) > 0 {
			_ = json.Unmarshal(b, dst)
		}
	}
	decode("toRecipients", &v.To)
	decode("ccRecipients", &v.CC)
	decode("bccRecipients", &v.BCC)
	decode("replyTo", &v.ReplyTo)
	decode("receivedDateTime", &v.Received)
	decode("sentDateTime", &v.Sent)
	decode("createdDateTime", &v.Created)
	decode("lastModifiedDateTime", &v.Modified)
	m := domain.MailMessage{ID: v.ID, InternetMessageID: v.InternetMessageID, ConversationID: v.ConversationID, ConversationIndex: v.ConversationIndex, FolderID: folderID, Subject: v.Subject, BodyPreview: v.BodyPreview, BodyContentType: v.Body.ContentType, SenderAddress: v.Sender.Email.Address, SenderName: v.Sender.Email.Name, FromAddress: v.From.Email.Address, FromName: v.From.Email.Name, Importance: v.Importance, FlagStatus: v.Flag.Status, WebURL: v.WebURL, ETag: v.ETag, Read: v.Read, Draft: v.Draft, HasAttachments: v.HasAttachments, RawJSON: append([]byte(nil), raw...), Categories: v.Categories}
	if strings.EqualFold(v.Body.ContentType, "html") {
		m.BodyHTML, m.BodyText = v.Body.Content, textutil.PlainHTML(v.Body.Content)
	} else {
		m.BodyText = v.Body.Content
	}
	m.ReceivedAt, m.SentAt, m.CreatedAt, m.ModifiedAt = parseTime(v.Received), parseTime(v.Sent), parseTime(v.Created), parseTime(v.Modified)
	for typ, parties := range map[string][]party{"to": v.To, "cc": v.CC, "bcc": v.BCC, "reply_to": v.ReplyTo} {
		for _, p := range parties {
			m.Recipients = append(m.Recipients, domain.MailRecipient{Type: typ, Address: p.Email.Address, Name: p.Email.Name, NormalizedAddress: NormalizeAddress(p.Email.Address)})
		}
	}
	for _, h := range v.Headers {
		m.Headers = append(m.Headers, domain.MailHeader{Name: h.Name, Value: h.Value})
	}
	for _, a := range v.Attachments {
		b, _ := json.Marshal(a)
		m.Attachments = append(m.Attachments, domain.MailAttachment{ID: a.ID, Name: a.Name, ContentType: a.ContentType, ContentID: a.ContentID, Type: a.ODataType, Size: a.Size, Inline: a.Inline, RawJSON: b})
	}
	return m, nil
}

func parseTime(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}
	return &t
}
