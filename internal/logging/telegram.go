package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"shopify-exporter/internal/config"
	"strings"
	"time"
)

type LoggerService interface {
	Log(value string)
	LogError(value string, err error)
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

type telegramErrorResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

const (
	iconInfo    = "ℹ️"
	iconError   = "❌"
	iconWarning = "⚠️"
	iconSuccess = "✅"
)

func NewLogger(cfg config.TelegramBotConfig) LoggerService {
	output := strings.ToLower(strings.TrimSpace(cfg.LogOutput))
	if output == "" {
		output = "stdout"
	}

	stdout := &stdoutLogger{}
	telegram := LoggerService(nil)
	if cfg.ChatId == "" || cfg.Token == "" {
		if output == "telegram" || output == "both" {
			fmt.Println("[WARNING]: telegram credentials missing")
		}
	} else {
		telegram = &Creds{Creds: cfg}
	}

	switch output {
	case "stdout":
		return stdout
	case "telegram":
		if telegram == nil {
			return stdout
		}
		return telegram
	case "both":
		if telegram == nil {
			return stdout
		}
		return &multiLogger{loggers: []LoggerService{stdout, telegram}}
	case "none":
		return nil
	default:
		fmt.Printf("[WARNING]: unknown LOG_OUTPUT=%q, defaulting to stdout\n", cfg.LogOutput)
		return stdout
	}
}

func (c *Creds) Log(value string) {
	if c == nil {
		return
	}
	_ = c.sendRequest(formatMessage(iconInfo, "INFO", value))
}

func (c *Creds) LogError(value string, err error) {
	if c == nil {
		return
	}
	msg := value
	if err != nil {
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%s\nerror: %s", msg, err.Error())
		}
	}
	_ = c.sendRequest(formatMessage(iconError, "ERROR", msg))
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

	for attempt := 0; attempt < 2; attempt++ {
		resp, err := http.Post(url, "application/json", bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			var errResp telegramErrorResponse
			if json.Unmarshal(respBody, &errResp) == nil && errResp.Parameters.RetryAfter > 0 {
				time.Sleep(time.Duration(errResp.Parameters.RetryAfter) * time.Second)
				continue
			}
		}

		fmt.Printf("failed: %s\n%s\n", resp.Status, string(respBody))
		return fmt.Errorf("telegram send failed: %s", resp.Status)
	}

	return fmt.Errorf("telegram send failed: too many requests")
}

type stdoutLogger struct{}

func (s *stdoutLogger) Log(value string) {
	fmt.Println(formatStdMessage("INFO", value))
}

func (s *stdoutLogger) LogError(value string, err error) {
	msg := value
	if err != nil {
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%s\nerror: %s", msg, err.Error())
		}
	}
	fmt.Println(formatStdMessage("ERROR", msg))
}

func (s *stdoutLogger) LogWarning(value string) {
	fmt.Println(formatStdMessage("WARNING", value))
}

func (s *stdoutLogger) LogSuccess(value string) {
	fmt.Println(formatStdMessage("SUCCESS", value))
}

type multiLogger struct {
	loggers []LoggerService
}

func (m *multiLogger) Log(value string) {
	for _, logger := range m.loggers {
		if logger != nil {
			logger.Log(value)
		}
	}
}

func (m *multiLogger) LogError(value string, err error) {
	for _, logger := range m.loggers {
		if logger != nil {
			logger.LogError(value, err)
		}
	}
}

func (m *multiLogger) LogWarning(value string) {
	for _, logger := range m.loggers {
		if logger != nil {
			logger.LogWarning(value)
		}
	}
}

func (m *multiLogger) LogSuccess(value string) {
	for _, logger := range m.loggers {
		if logger != nil {
			logger.LogSuccess(value)
		}
	}
}

func formatStdMessage(level, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		v = "-"
	}
	return fmt.Sprintf("[%s] %s", level, v)
}
