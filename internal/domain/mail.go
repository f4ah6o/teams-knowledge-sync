package domain

import "time"

type MailFolder struct {
	ID, DisplayName, WellKnownName, ParentID string
	TotalCount, UnreadCount                  int
	Enabled                                  bool
}
type MailMessage struct {
	ID, InternetMessageID, ConversationID, ConversationIndex, FolderID         string
	Subject, BodyHTML, BodyText, BodyPreview                                   string
	FromAddress, FromName, SenderAddress, SenderName, Importance, WebURL, ETag string
	IsRead, IsDraft, HasAttachments                                            bool
	ReceivedAt, SentAt                                                         time.Time
	CreatedAt, ModifiedAt, DeletedAt                                           *time.Time
	RawJSON                                                                    []byte
	Recipients                                                                 []MailRecipient
	Headers                                                                    []MailHeader
	Attachments                                                                []MailAttachment
	Categories                                                                 []string
	Matches                                                                    []AddressMatch
}
type MailRecipient struct {
	Type, Address, Name, Normalized string
}
type MailHeader struct {
	Name, Value string
}
type MailAttachment struct {
	ID, Name, ContentType string
	Size                  int64
	IsInline              bool
}
type RegisteredAddress struct {
	ID                       int64
	Address, Name            string
	Enabled                  bool
	Headers, SubjectPrefixes []string
}
type AddressMatch struct {
	RegisteredID            int64
	MatchedBy, MatchedValue string
}
type MailSearchFilter struct {
	Query, Address, Sender, FolderID string
	From, To                         *time.Time
	Limit                            int
}
type MailSearchResult struct {
	ID, FolderID, FolderName, FromAddress, FromName, Subject, Snippet, WebURL string
	ReceivedAt                                                                time.Time
}
