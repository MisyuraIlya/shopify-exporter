package config

import "time"

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
}
