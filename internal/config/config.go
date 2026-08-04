package config

import "time"

// DefaultUntrackedSkuPrefixes is the fallback for SHOPIFY_UNTRACKED_SKU_PREFIXES.
// ZZ-* are the Hashavshevet placeholder/service items for Emanuel.
var DefaultUntrackedSkuPrefixes = []string{"ZZ-"}

type DailyConfig struct {
	Shopify     ShopifyConfig
	ApiHasav    ApiHasvConfig
	TelegramBot TelegramBotConfig
	Report      ReportConfig
}

// ReportConfig controls the per-run email report. A run always tries to send one,
// so an empty inbox is itself the signal that the scheduled job never started.
type ReportConfig struct {
	// Enabled is REPORT_EMAIL_ENABLED (default true); the report is still skipped,
	// with a warning, when the SMTP settings or recipients are incomplete.
	Enabled bool
	// Recipients is REPORT_EMAIL_TO, comma separated.
	Recipients []string
	// MaxRows caps how many rows of each table are inlined in the email body; the
	// attached CSV always carries every row.
	MaxRows int
	// Timezone renders report timestamps for a human reader (e.g. Asia/Jerusalem).
	Timezone string
	SMTP     SMTPConfig
}

// SMTPConfig is the relay used to deliver reports.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	Timeout  time.Duration
	// ImplicitTLS dials TLS directly instead of upgrading with STARTTLS. Defaults
	// to true for port 465.
	ImplicitTLS bool
	// SkipTLSVerify is an escape hatch for a broken relay certificate.
	SkipTLSVerify bool
}

// Configured reports whether the report can actually be delivered.
func (c ReportConfig) Configured() bool {
	return c.SMTP.Host != "" && c.SMTP.From != "" && len(c.Recipients) > 0
}

type OrdersConfig struct {
	Mysql       MysqlConfig
	TelegramBot TelegramBotConfig
	Shopify     ShopifyConfig
	ApiHasav    ApiHasvConfig
}

type ShopifyConfig struct {
	ShopDomain string
	Token      string
	APIVer     string
	Timeout    time.Duration
	// UntrackedSkuPrefixes are SKU prefixes that must NOT get Shopify inventory
	// tracking. These are ERP service/placeholder items (ZZ-*: "נא פנו אלינו לביצוע
	// הזמנה", "תשלום בכרטיס אשראי - הזמנות מיוחדות") that carry no stock in
	// Hashavshevet and never appear in /stocksProducts. Tracking them would pin them
	// at quantity 0 and make them unbuyable, breaking the special-order flow.
	UntrackedSkuPrefixes []string
	// Optional pricing settings used by price sync.
	BaseCurrency               string
	InternationalMarketHandle  string
	InternationalMarketName    string
	InternationalCatalogTitle  string
	InternationalPriceListName string
}

type MysqlConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
}

type ApiHasvConfig struct {
	BaseUrl string
	Token   string
	Timeout time.Duration
}

type TelegramBotConfig struct {
	ChatId string
	Token  string
	// LogOutput controls logger targets: stdout, telegram, both, none.
	LogOutput string
	// LogFileDir stores one log file per run when set.
	LogFileDir string
}
