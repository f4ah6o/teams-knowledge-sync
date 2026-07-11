package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obr-grp/teams-knowledge-sync/internal/domain"
	"github.com/obr-grp/teams-knowledge-sync/internal/graph"
	"github.com/obr-grp/teams-knowledge-sync/internal/outlookstore"
	"github.com/obr-grp/teams-knowledge-sync/internal/text"
)

type GraphAPI interface {
	Do(ctx context.Context, method, path string, body any, out any) error
	GetPage(ctx context.Context, pageURL string, headers map[string]string) (graph.PageResult, error)
}

type Service struct {
	Graph           GraphAPI
	Store           *outlookstore.Store
	Addresses       []domain.RegisteredAddress
	IncludeReceived bool
	IncludeSent     bool
	FolderInclude   []string
	FolderExclude   []string
	LookbackDays    int
	Now             func() time.Time
}

const messageSelect = "id,internetMessageId,conversationId,conversationIndex,subject,body,bodyPreview,from,sender,toRecipients,ccRecipients,bccRecipients,replyTo,receivedDateTime,sentDateTime,createdDateTime,lastModifiedDateTime,isRead,isDraft,importance,categories,hasAttachments,webLink,parentFolderId,internetMessageHeaders"

func headers() map[string]string {
	return map[string]string{"Prefer": `IdType="ImmutableId", outlook.body-content-type="html"`}
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *Service) lookback() time.Time {
	days := s.LookbackDays
	if days == 0 {
		days = 365
	}
	return s.now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
}

type folderResource struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName"`
	ParentFolderID  string `json:"parentFolderId"`
	TotalItemCount  int    `json:"totalItemCount"`
	UnreadItemCount int    `json:"unreadItemCount"`
}

// ResolveFolders resolves configured include folders (well-known names or raw
// IDs) into concrete folders, dropping anything that also resolves from the
// exclude list. Excluded folders are never fetched.
func (s *Service) ResolveFolders(ctx context.Context) ([]domain.MailFolder, error) {
	excluded := map[string]bool{}
	for _, token := range s.FolderExclude {
		f, err := s.getFolder(ctx, token)
		if err != nil {
			continue
		}
		excluded[f.ID] = true
	}
	var out []domain.MailFolder
	seen := map[string]bool{}
	for _, token := range s.FolderInclude {
		f, err := s.getFolder(ctx, token)
		if err != nil {
			log.Printf("mail folder %q: %v", token, err)
			continue
		}
		if excluded[f.ID] || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		folder := domain.MailFolder{ID: f.ID, DisplayName: f.DisplayName, ParentID: f.ParentFolderID, TotalCount: f.TotalItemCount, UnreadCount: f.UnreadItemCount, Enabled: true}
		if isWellKnown(token) {
			folder.WellKnownName = strings.ToLower(token)
		}
		if err = s.Store.UpsertMailFolder(ctx, folder); err != nil {
			return nil, err
		}
		out = append(out, folder)
	}
	return out, nil
}

// ResolveFolder resolves a single folder token (well-known name or raw ID).
func (s *Service) ResolveFolder(ctx context.Context, token string) (domain.MailFolder, error) {
	f, err := s.getFolder(ctx, token)
	if err != nil {
		return domain.MailFolder{}, err
	}
	folder := domain.MailFolder{ID: f.ID, DisplayName: f.DisplayName, ParentID: f.ParentFolderID, TotalCount: f.TotalItemCount, UnreadCount: f.UnreadItemCount, Enabled: true}
	if isWellKnown(token) {
		folder.WellKnownName = strings.ToLower(token)
	}
	return folder, s.Store.UpsertMailFolder(ctx, folder)
}

// SyncOne registers configured addresses and syncs a single folder.
func (s *Service) SyncOne(ctx context.Context, f domain.MailFolder) error {
	regs, err := s.Store.SyncRegisteredAddresses(ctx, s.Addresses)
	if err != nil {
		return err
	}
	s.Addresses = regs
	return s.SyncFolder(ctx, f)
}
func (s *Service) getFolder(ctx context.Context, token string) (folderResource, error) {
	var f folderResource
	err := s.Graph.Do(ctx, http.MethodGet, "me/mailFolders/"+graph.Escape(token)+"?$select=id,displayName,parentFolderId,totalItemCount,unreadItemCount", nil, &f)
	return f, err
}
func isWellKnown(token string) bool {
	for _, r := range token {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return token != ""
}
func (s *Service) skipFolder(f domain.MailFolder) bool {
	if f.WellKnownName == "sentitems" {
		return !s.IncludeSent
	}
	return !s.IncludeReceived
}

// SyncAll registers configured addresses, resolves folders, and syncs each
// folder, isolating per-folder failures.
func (s *Service) SyncAll(ctx context.Context) error {
	regs, err := s.Store.SyncRegisteredAddresses(ctx, s.Addresses)
	if err != nil {
		return err
	}
	s.Addresses = regs
	folders, err := s.ResolveFolders(ctx)
	if err != nil {
		return err
	}
	failures := 0
	for _, f := range folders {
		if s.skipFolder(f) {
			continue
		}
		if err := s.SyncFolder(ctx, f); err != nil {
			failures++
			log.Printf("mail folder %s (%s): %v", f.DisplayName, f.ID, err)
		}
	}
	if failures > 0 && failures == len(folders) {
		return fmt.Errorf("all %d mail folders failed", failures)
	}
	return nil
}

// SyncFolder runs the folder's delta state machine: resume a stored nextLink,
// else continue from the stored deltaLink, else start a full delta
// initialization limited by the lookback period. The deltaLink is committed
// only after every page has been applied.
func (s *Service) SyncFolder(ctx context.Context, f domain.MailFolder) error {
	return s.syncFolderDelta(ctx, f, true)
}
func (s *Service) syncFolderDelta(ctx context.Context, f domain.MailFolder, allowReset bool) error {
	started := s.now().UTC()
	st, err := s.Store.GetMailSyncState(ctx, f.ID)
	if err != nil {
		return err
	}
	pageURL := st.NextLink
	if pageURL == "" {
		pageURL = st.DeltaLink
	}
	if pageURL == "" {
		pageURL = s.deltaInitURL(f)
	}
	for {
		page, err := s.Graph.GetPage(ctx, pageURL, headers())
		if err != nil {
			if graph.IsSyncStateInvalid(err) && allowReset {
				// expired delta token: reinitialize this folder only
				if e := s.Store.ResetMailSyncState(ctx, f.ID); e != nil {
					return e
				}
				return s.syncFolderDelta(ctx, f, false)
			}
			_ = s.Store.RecordMailSyncFailure(ctx, f.ID, started, err)
			return err
		}
		for _, raw := range page.Value {
			if err := s.applyDeltaItem(ctx, raw, f.ID); err != nil {
				_ = s.Store.RecordMailSyncFailure(ctx, f.ID, started, err)
				return err
			}
		}
		if page.NextLink != "" {
			if err := s.Store.SaveMailNextLink(ctx, f.ID, page.NextLink); err != nil {
				return err
			}
			pageURL = page.NextLink
			continue
		}
		deltaLink := page.DeltaLink
		if deltaLink == "" {
			deltaLink = st.DeltaLink
		}
		return s.Store.CommitMailDeltaLink(ctx, f.ID, deltaLink, started, s.now().UTC())
	}
}
func (s *Service) deltaInitURL(f domain.MailFolder) string {
	cutoff := s.lookback().Format(time.RFC3339)
	return "me/mailFolders/" + graph.Escape(f.ID) + "/messages/delta?$filter=" + url.QueryEscape("receivedDateTime ge "+cutoff) + "&$select=" + messageSelect
}

// applyDeltaItem reflects one delta entry: @removed tombstones the message in
// this folder; anything else (adds, updates, read-state changes, moves into
// this folder) is upserted.
func (s *Service) applyDeltaItem(ctx context.Context, raw json.RawMessage, folderID string) error {
	var probe struct {
		ID      string `json:"id"`
		Removed *struct {
			Reason string `json:"reason"`
		} `json:"@removed"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	if probe.Removed != nil {
		return s.Store.TombstoneMail(ctx, probe.ID, folderID, s.now().UTC())
	}
	return s.storeMatched(ctx, raw, folderID)
}

// ResetFolder clears the folder's delta state so the next sync reinitializes.
func (s *Service) ResetFolder(ctx context.Context, folderID string) error {
	return s.Store.ResetMailSyncState(ctx, folderID)
}

// storeMatched parses, classifies, and persists one message when it matches a
// registered address. Unmatched messages are not stored.
func (s *Service) storeMatched(ctx context.Context, raw json.RawMessage, folderID string) error {
	m, err := Message(raw, folderID)
	if err != nil {
		return err
	}
	m.Matches = Classify(&m, s.Addresses)
	if len(m.Matches) == 0 && len(m.Headers) == 0 && s.hasHeaderRules() {
		// delta payloads may omit internetMessageHeaders; fetch them once
		// before deciding the message is unmatched
		if err := s.fetchHeaders(ctx, &m); err != nil {
			return err
		}
		m.Matches = Classify(&m, s.Addresses)
	}
	if len(m.Matches) == 0 {
		return nil
	}
	if m.HasAttachments {
		att, err := s.fetchAttachments(ctx, m.ID)
		if err != nil {
			return err
		}
		m.Attachments = att
	}
	return s.Store.UpsertMailMessage(ctx, m)
}
func (s *Service) hasHeaderRules() bool {
	for _, r := range s.Addresses {
		if r.Enabled && len(r.Headers) > 0 {
			return true
		}
	}
	return false
}
func (s *Service) fetchHeaders(ctx context.Context, m *domain.MailMessage) error {
	var v struct {
		InternetMessageHeaders []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"internetMessageHeaders"`
	}
	if err := s.Graph.Do(ctx, http.MethodGet, "me/messages/"+graph.Escape(m.ID)+"?$select=internetMessageHeaders", nil, &v); err != nil {
		return err
	}
	for _, h := range v.InternetMessageHeaders {
		m.Headers = append(m.Headers, domain.MailHeader{Name: h.Name, Value: h.Value})
	}
	return nil
}
func (s *Service) fetchAttachments(ctx context.Context, id string) ([]domain.MailAttachment, error) {
	pageURL := "me/messages/" + graph.Escape(id) + "/attachments?$select=" + url.QueryEscape("id,name,contentType,size,isInline")
	var out []domain.MailAttachment
	for pageURL != "" {
		page, err := s.Graph.GetPage(ctx, pageURL, headers())
		if err != nil {
			return nil, err
		}
		for _, raw := range page.Value {
			var a struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				ContentType string `json:"contentType"`
				Size        int64  `json:"size"`
				IsInline    bool   `json:"isInline"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			out = append(out, domain.MailAttachment{ID: a.ID, Name: a.Name, ContentType: a.ContentType, Size: a.Size, IsInline: a.IsInline})
		}
		pageURL = page.NextLink
	}
	return out, nil
}

type emailAddress struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}
type recipient struct {
	EmailAddress emailAddress `json:"emailAddress"`
}

// Message maps a Graph message payload onto the domain model. Body text is
// normalized from HTML; recipient addresses are pre-normalized for matching.
func Message(raw json.RawMessage, folderID string) (domain.MailMessage, error) {
	var v struct {
		ID                string      `json:"id"`
		InternetMessageID string      `json:"internetMessageId"`
		ConversationID    string      `json:"conversationId"`
		ConversationIndex string      `json:"conversationIndex"`
		Subject           string      `json:"subject"`
		BodyPreview       string      `json:"bodyPreview"`
		Importance        string      `json:"importance"`
		WebLink           string      `json:"webLink"`
		ETag              string      `json:"@odata.etag"`
		ParentFolderID    string      `json:"parentFolderId"`
		IsRead            bool        `json:"isRead"`
		IsDraft           bool        `json:"isDraft"`
		HasAttachments    bool        `json:"hasAttachments"`
		Received          string      `json:"receivedDateTime"`
		Sent              string      `json:"sentDateTime"`
		Created           string      `json:"createdDateTime"`
		Modified          string      `json:"lastModifiedDateTime"`
		Categories        []string    `json:"categories"`
		From              recipient   `json:"from"`
		Sender            recipient   `json:"sender"`
		To                []recipient `json:"toRecipients"`
		Cc                []recipient `json:"ccRecipients"`
		Bcc               []recipient `json:"bccRecipients"`
		ReplyTo           []recipient `json:"replyTo"`
		Body              struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		InternetMessageHeaders []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"internetMessageHeaders"`
	}
	if e := json.Unmarshal(raw, &v); e != nil {
		return domain.MailMessage{}, e
	}
	if v.ID == "" {
		return domain.MailMessage{}, fmt.Errorf("mail message without id")
	}
	if v.ParentFolderID != "" {
		folderID = v.ParentFolderID
	}
	m := domain.MailMessage{ID: v.ID, InternetMessageID: v.InternetMessageID, ConversationID: v.ConversationID, ConversationIndex: v.ConversationIndex, FolderID: folderID, Subject: v.Subject, BodyPreview: v.BodyPreview, Importance: v.Importance, WebURL: v.WebLink, ETag: v.ETag, IsRead: v.IsRead, IsDraft: v.IsDraft, HasAttachments: v.HasAttachments, FromAddress: v.From.EmailAddress.Address, FromName: v.From.EmailAddress.Name, SenderAddress: v.Sender.EmailAddress.Address, SenderName: v.Sender.EmailAddress.Name, Categories: v.Categories, RawJSON: raw}
	if strings.EqualFold(v.Body.ContentType, "html") {
		m.BodyHTML = v.Body.Content
		m.BodyText = text.PlainHTML(v.Body.Content)
	} else {
		m.BodyText = v.Body.Content
	}
	if t, e := time.Parse(time.RFC3339, v.Received); e == nil {
		m.ReceivedAt = t
	}
	if t, e := time.Parse(time.RFC3339, v.Sent); e == nil {
		m.SentAt = t
	}
	if t, e := time.Parse(time.RFC3339, v.Created); e == nil {
		m.CreatedAt = &t
	}
	if t, e := time.Parse(time.RFC3339, v.Modified); e == nil {
		m.ModifiedAt = &t
	}
	for _, group := range []struct {
		typ  string
		list []recipient
	}{{"to", v.To}, {"cc", v.Cc}, {"bcc", v.Bcc}, {"reply_to", v.ReplyTo}} {
		for _, r := range group.list {
			m.Recipients = append(m.Recipients, domain.MailRecipient{Type: group.typ, Address: r.EmailAddress.Address, Name: r.EmailAddress.Name, Normalized: NormalizeAddress(r.EmailAddress.Address)})
		}
	}
	for _, h := range v.InternetMessageHeaders {
		m.Headers = append(m.Headers, domain.MailHeader{Name: h.Name, Value: h.Value})
	}
	return m, nil
}
