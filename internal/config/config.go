package config

import "time"

type Config struct {
	Shopify     ShopifyConfig
	Mysql       MysqlConfig
	ApiHasav    ApiHasvConfig
	TelegramBot TelegramBotConfig
}

type ShopifyConfig struct {
	BaseUrl string
	Token   string
	Timeout time.Duration
}

type MysqlConfig struct {
	Host     string
	Port     int8
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
}
