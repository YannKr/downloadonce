package model

import "time"

type Account struct {
	ID                string
	Email             string
	Name              string
	PasswordHash      string
	Role              string // "admin" or "member"
	Enabled           bool
	NotifyOnDownload  bool
	CreatedAt         time.Time
}

type Session struct {
	ID        string
	AccountID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Asset struct {
	ID           string
	AccountID    string
	OriginalName string
	AssetType    string // "video" or "image"
	OriginalPath string
	FileSize     int64
	SHA256       string
	MimeType     string
	Duration     *float64
	Width        *int64
	Height       *int64
	CreatedAt    time.Time
}

type Recipient struct {
	ID        string
	AccountID string
	Name      string
	Email     string
	Org       string
	CreatedAt time.Time
}

type Campaign struct {
	ID           string
	AccountID    string
	AssetID      string
	Name         string
	MaxDownloads *int
	ExpiresAt    *time.Time
	VisibleWM    bool
	InvisibleWM  bool
	State        string // DRAFT, PROCESSING, READY, EXPIRED
	CreatedAt    time.Time
	PublishedAt  *time.Time
}

type CampaignSummary struct {
	Campaign
	AssetName       string
	AssetType       string
	RecipientCount  int
	DownloadedCount int
	JobsTotal       int
	JobsCompleted   int
	JobsFailed      int
	CreatorName     string
}

type DownloadToken struct {
	ID              string
	CampaignID      string
	RecipientID     string
	MaxDownloads    *int
	DownloadCount   int
	State           string // PENDING, ACTIVE, CONSUMED, EXPIRED
	WatermarkedPath *string
	WatermarkPayload []byte
	SHA256Output    *string
	OutputSizeBytes *int64
	ExpiresAt       *time.Time
	CreatedAt       time.Time
}

type TokenWithRecipient struct {
	DownloadToken
	RecipientName  string
	RecipientEmail string
	RecipientOrg   string
	LastDownloadAt *time.Time
	DownloadEvents []DownloadEvent
}

type DownloadEvent struct {
	ID          string
	TokenID     string
	CampaignID  string
	RecipientID string
	AssetID     string
	IPAddress   string
	UserAgent   string
	CreatedAt   time.Time
}

type Job struct {
	ID           string
	JobType      string // "watermark_video", "watermark_image", "detect"
	CampaignID   string
	TokenID      string
	State        string // PENDING, RUNNING, COMPLETED, FAILED
	Progress     int
	ErrorMessage string
	InputPath    string // for detect jobs: path to the uploaded suspect file
	ResultData   string // for detect jobs: JSON with detection results
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

type APIKey struct {
	ID         string
	AccountID  string
	Name       string
	KeyPrefix  string
	KeyHash    string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type Webhook struct {
	ID        string
	AccountID string
	URL       string
	Secret    string
	Events    string // comma-separated: "download", "campaign_ready"
	Enabled   bool
	CreatedAt time.Time
}

type UploadSession struct {
	ID             string
	AccountID      string
	Filename       string
	Size           int64
	MimeType       string
	ChunkSize      int64
	TotalChunks    int
	ReceivedChunks []int
	Status         string // PENDING, COMPLETE, EXPIRED
	StoragePath    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
}

type RecipientGroup struct {
	ID          string
	AccountID   string
	Name        string
	Description string
	CreatedAt   time.Time
}

type RecipientGroupSummary struct {
	RecipientGroup
	MemberCount int
}

type RecipientGroupMember struct {
	Recipient
	AddedAt time.Time
}

type GroupBadge struct {
	ID   string
	Name string
}

type RecipientWithGroups struct {
	Recipient
	Groups []GroupBadge
}
