package config

import "time"

// DefaultUntrackedSkuPrefixes is the fallback for SHOPIFY_UNTRACKED_SKU_PREFIXES.
// ZZ-* are the Hashavshevet placeholder/service items for Emanuel.
var DefaultUntrackedSkuPrefixes = []string{"ZZ-"}

type DailyConfig struct {
	Shopify     ShopifyConfig
	ApiHasav    ApiHasvConfig
	TelegramBot TelegramBotConfig
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
