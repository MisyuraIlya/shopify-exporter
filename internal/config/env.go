package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func requriedString(key string) (string, error) {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return "", fmt.Errorf("missing requried env var: %s", key)
	}
	return variable, nil
}

func stringWithDefault(key, def string) string {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return def
	}
	return variable
}

func intWithDefault(key string, def int) (int, error) {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return def, nil
	}
	number, err := strconv.Atoi(variable)
	if err != nil {
		return 0, fmt.Errorf("Invalid int for %s: %w", key, err)
	}
	return number, nil
}

func durationWithDefualt(key string, def time.Duration) (time.Duration, error) {
	variable, isOk := os.LookupEnv(key)
	if !isOk || variable == "" {
		return def, nil
	}

	number, err := strconv.Atoi(variable)
	if err != nil {
		return 0, fmt.Errorf("Invalid duration for %s: %w", key, err)
	}
	return time.Duration(number) * time.Millisecond, nil
}

func LoadForDailySync() (*DailyConfig, error) {
	shopifyBaseUrl, err := requriedString("SHOPIFY_SHOP_DOMAIN")
	if err != nil {
		return nil, err
	}
	shopifyToken, err := requriedString("SHOPIFY_ACCESS_TOKEN")
	if err != nil {
		return nil, err
	}

	shopifyDuration, err := durationWithDefualt("SHOPIFT_DURATION_MS", 5000)
	if err != nil {
		return nil, err
	}

	cfgShopify := ShopifyConfig{
		ShopDomain: shopifyBaseUrl,
		Token:      shopifyToken,
		Timeout:    shopifyDuration,
	}

	hasavBaseUrl, err := requriedString("API_BASE_URL")

	if err != nil {
		return nil, err
	}

	hasavToken, err := requriedString("API_TOKEN")

	if err != nil {
		return nil, err
	}

	hasavDuration, err := durationWithDefualt("API_DURATION_MS", 10000)

	if err != nil {
		return nil, err
	}

	cpfHasav := ApiHasvConfig{
		BaseUrl: hasavBaseUrl,
		Token:   hasavToken,
		Timeout: hasavDuration,
	}

	cfgDaily := &DailyConfig{
		Shopify:  cfgShopify,
		ApiHasav: cpfHasav,
	}
	cfgDaily.TelegramBot.ChatId = stringWithDefault("TELEGRAM_CHAT_ID", "")
	cfgDaily.TelegramBot.Token = stringWithDefault("TELEGRAM_TOKEN", "")
	return cfgDaily, nil
}

func LoadForSyncOrder() (*OrdersConfig, error) {
	shopifyBaseUrl, err := requriedString("SHOPIFY_SHOP_DOMAIN")
	if err != nil {
		return nil, err
	}
	shopifyToken, err := requriedString("SHOPIFY_ACCESS_TOKEN")
	if err != nil {
		return nil, err
	}

	shopifyDuration, err := durationWithDefualt("SHOPIFT_DURATION_MS", 5000)
	if err != nil {
		return nil, err
	}

	cfgShopify := ShopifyConfig{
		ShopDomain: shopifyBaseUrl,
		Token:      shopifyToken,
		Timeout:    shopifyDuration,
	}

	hasavBaseUrl, err := requriedString("API_BASE_URL")

	if err != nil {
		return nil, err
	}

	hasavToken, err := requriedString("API_TOKEN")

	if err != nil {
		return nil, err
	}

	hasavDuration, err := durationWithDefualt("API_DURATION_MS", 10000)

	if err != nil {
		return nil, err
	}

	cpfHasav := ApiHasvConfig{
		BaseUrl: hasavBaseUrl,
		Token:   hasavToken,
		Timeout: hasavDuration,
	}

	mySqlHost, err := requriedString("MYSQL_HOST")
	if err != nil {
		return nil, err
	}
	mySqlPort, err := intWithDefault("MYSQL_PORT", 3306)
	if err != nil {
		return nil, err
	}
	mySqlUser, err := requriedString("MYSQL_USER")
	if err != nil {
		return nil, err
	}
	mySqlPassword, err := requriedString("MYSQL_PASSWORD")
	if err != nil {
		return nil, err
	}
	mySqlDatabase, err := requriedString("MYSQL_DATABASE")
	if err != nil {
		return nil, err
	}

	cfgMysql := MysqlConfig{
		Host:     mySqlHost,
		Port:     mySqlPort,
		Username: mySqlUser,
		Password: mySqlPassword,
		Database: mySqlDatabase,
	}

	cfgOrd := &OrdersConfig{
		Shopify:  cfgShopify,
		ApiHasav: cpfHasav,
		Mysql:    cfgMysql,
	}

	cfgOrd.TelegramBot.ChatId = stringWithDefault("TELEGRAM_CHAT_ID", "")
	cfgOrd.TelegramBot.Token = stringWithDefault("TELEGRAM_TOKEN", "")

	return cfgOrd, nil
}
