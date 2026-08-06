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
	Stock       StockConfig
}

// Stock sync modes for SYNC_STOCK_MODE.
const (
	// StockModeFull pushes the whole ERP feed, skipping only SKUs Shopify already
	// holds at the right quantity. This is the reconciliation pass: it is the run that
	// corrects drift Shopify caused on its own (a fulfilment moving on_hand).
	StockModeFull = "full"
	// StockModeDelta pushes only SKUs whose ERP quantity changed since the last
	// successful run, read from the on-disk snapshot. Cheap enough for a cron that
	// fires every few minutes; relies on a periodic full run to catch drift.
	StockModeDelta = "delta"
)

// StockConfig controls how much of the ERP feed a stock run pushes.
type StockConfig struct {
	// Mode is StockModeFull (default) or StockModeDelta. An unrecognised value falls
	// back to full: pushing too much is slow, pushing too little oversells.
	Mode string
	// StatePath is where the delta snapshot lives. Defaults to stock-state.json under
	// LOG_FILE_DIR, which is the volume-mounted directory in the container, so the
	// snapshot survives the one-shot `docker run` that produced it.
	StatePath string
	// DryRun resolves everything and reports exactly what would move, without writing
	// a single mutation to Shopify. Reads still happen — that is what produces the
	// before -> after list. The snapshot is deliberately not written either, so a dry
	// run cannot make the next real delta believe those quantities were pushed.
	DryRun bool
}

// IsDelta reports whether this run should push only ERP changes.
func (c StockConfig) IsDelta() bool {
	return c.Mode == StockModeDelta
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
	// OnlyOnChange suppresses the email when a run succeeded and changed nothing. Off
	// by default, because "no mail in the inbox = the job never ran" is the alert for
	// the scheduled full sync.
	OnlyOnChange bool
	// OnlyOnFailure suppresses the email unless a step actually failed, and overrides
	// OnlyOnChange when both are set.
	//
	// This is what the five-minute delta cron uses. OnlyOnChange was not enough: in a
	// live catalogue almost every tick moves at least one SKU, so it still produced
	// ~288 mails a day. A reader who has to skim 288 "3 items changed" mails is a
	// reader who stops opening them, which is the same as no alert at all. Failures
	// remain immediate; the once-a-day summary comes from the full run, which mails
	// unconditionally.
	OnlyOnFailure bool
	SMTP          SMTPConfig
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
	// StockDryRun mirrors StockConfig.DryRun. It lives here too because the adapter is
	// where the mutations are, and it must be the thing that refuses to send them —
	// enforcing it only in the use case would leave every other caller of
	// SetOnHandQuantities able to write during a dry run. Both fields are filled from
	// one read of SYNC_STOCK_DRY_RUN.
	StockDryRun bool
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
