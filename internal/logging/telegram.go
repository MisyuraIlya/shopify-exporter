package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/config"
	"strings"
)

type LoggerService interface {
	Log(value string)
	LogError(value string)
	LogWarning(value string)
	LogSuccess(value string)
}

type Creds struct {
	Creds config.TelegramBotConfig
}

type telegramRequest struct {
	ChatId string `json:"chat_id"`
	Text   string `json:"text"`
}

const (
	iconInfo    = "ℹ️"
	iconError   = "❌"
	iconWarning = "⚠️"
	iconSuccess = "✅"
)

func NewLogger(cfg *Creds) LoggerService {
	if cfg == nil || cfg.Creds.ChatId == "" || cfg.Creds.Token == "" {
		fmt.Println("[WARNING]: telegram credentials missing")
		return nil
	}
	return &Creds{Creds: cfg.Creds}
}

func (c *Creds) Log(value string) {
	if c == nil {
		return
	}
	_ = c.sendRequest(formatMessage(iconInfo, "INFO", value))
}

func (c *Creds) LogError(value string) {
	if c == nil {
		return
	}
	_ = c.sendRequest(formatMessage(iconError, "ERROR", value))
}

func (c *Creds) LogWarning(value string) {
	if c == nil {
		return
	}
	_ = c.sendRequest(formatMessage(iconWarning, "WARNING", value))
}

func (c *Creds) LogSuccess(value string) {
	if c == nil {
		return
	}
	_ = c.sendRequest(formatMessage(iconSuccess, "SUCCESS", value))
}

func formatMessage(icon, level, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		v = "-"
	}
	return fmt.Sprintf("%s %s: %s", icon, level, v)
}

func (c *Creds) sendRequest(value string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.Creds.Token)

	reqBody := telegramRequest{
		ChatId: c.Creds.ChatId,
		Text:   value,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Printf("failed: %s\n%s\n", resp.Status, string(respBody))
		return fmt.Errorf("telegram send failed: %s", resp.Status)
	}

	fmt.Println("ok:", string(respBody))
	return nil
}
