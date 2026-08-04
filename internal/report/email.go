package report

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig is everything needed to deliver one report.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	To       []string
	// Timeout bounds the whole SMTP conversation.
	Timeout time.Duration
	// ImplicitTLS dials TLS directly (port 465) instead of upgrading via STARTTLS.
	ImplicitTLS bool
	// SkipTLSVerify disables certificate verification. Escape hatch for a broken
	// relay certificate; leave false.
	SkipTLSVerify bool
}

// Validate reports whether the config can actually send.
func (c SMTPConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("smtp host is required")
	}
	if c.Port <= 0 {
		return errors.New("smtp port is required")
	}
	if strings.TrimSpace(c.From) == "" {
		return errors.New("smtp from address is required")
	}
	if len(c.recipients()) == 0 {
		return errors.New("at least one report recipient is required")
	}
	return nil
}

func (c SMTPConfig) recipients() []string {
	out := make([]string, 0, len(c.To))
	for _, addr := range c.To {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (c SMTPConfig) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 30 * time.Second
	}
	return c.Timeout
}

// SendEmail renders the summary and delivers it as a multipart message: an HTML
// body plus the full change list as a CSV attachment.
func SendEmail(summary Summary, cfg SMTPConfig, opts RenderOptions) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	message := buildMessage(summary, cfg, opts)
	return deliver(cfg, message)
}

func buildMessage(summary Summary, cfg SMTPConfig, opts RenderOptions) []byte {
	boundary := "shopify-report-" + randomToken()
	from := cfg.From
	if name := strings.TrimSpace(cfg.FromName); name != "" {
		from = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("UTF-8", name), cfg.From)
	}

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(cfg.recipients(), ", ") + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", summary.Subject(opts)) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	// Auto-generated mail: keep it out of vacation responders and reply loops.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Shopify-Sync-Job: " + summary.Job + "\r\n")
	b.WriteString("X-Shopify-Sync-Status: " + summary.Status() + "\r\n")
	b.WriteString(`Content-Type: multipart/mixed; boundary="` + boundary + `"` + "\r\n\r\n")

	// HTML part.
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(summary.HTML(opts))))
	b.WriteString("\r\n")

	// CSV attachment.
	csvBody := summary.CSV()
	if len(csvBody) > 0 {
		filename := summary.CSVFilename()
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/csv; charset=\"UTF-8\"; name=\"" + filename + "\"\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(wrapBase64(csvBody))
		b.WriteString("\r\n")
	}

	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

func deliver(cfg SMTPConfig, message []byte) error {
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	dialer := &net.Dialer{Timeout: cfg.timeout()}

	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.SkipTLSVerify, //nolint:gosec // opt-in escape hatch for a broken relay cert
		MinVersion:         tls.VersionTLS12,
	}

	var conn net.Conn
	var err error
	if cfg.ImplicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	// Bound every read/write on the connection, not just the dial.
	_ = conn.SetDeadline(time.Now().Add(cfg.timeout()))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Hello(heloName(cfg.From)); err != nil {
		return fmt.Errorf("smtp helo: %w", err)
	}

	if !cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}

	if strings.TrimSpace(cfg.Username) != "" {
		auth, err := pickAuth(client, cfg)
		if err != nil {
			return err
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth as %s: %w", cfg.Username, err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp mail from %s: %w", cfg.From, err)
	}
	for _, recipient := range cfg.recipients() {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt to %s: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return client.Quit()
}

// pickAuth chooses PLAIN when the server advertises it and falls back to LOGIN,
// which is what Office 365 negotiates in practice.
func pickAuth(client *smtp.Client, cfg SMTPConfig) (smtp.Auth, error) {
	ok, params := client.Extension("AUTH")
	if !ok {
		return nil, errors.New("smtp server does not advertise AUTH but credentials were supplied")
	}
	mechanisms := strings.ToUpper(params)
	switch {
	case strings.Contains(mechanisms, "PLAIN"):
		return smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host), nil
	case strings.Contains(mechanisms, "LOGIN"):
		return &loginAuth{username: cfg.Username, password: cfg.Password, host: cfg.Host}, nil
	default:
		return nil, fmt.Errorf("no supported smtp auth mechanism in %q", params)
	}
}

// loginAuth implements the non-standard but widely deployed AUTH LOGIN exchange.
type loginAuth struct {
	username string
	password string
	host     string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("smtp auth login requires a TLS connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("smtp auth login: unexpected server name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(strings.TrimSuffix(string(fromServer), ":"))) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("smtp auth login: unexpected server challenge %q", string(fromServer))
	}
}

// wrapBase64 encodes the payload and folds it to the 76-column MIME limit.
func wrapBase64(payload []byte) string {
	encoded := base64.StdEncoding.EncodeToString(payload)
	const lineLen = 76
	var b strings.Builder
	for start := 0; start < len(encoded); start += lineLen {
		end := start + lineLen
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[start:end])
		b.WriteString("\r\n")
	}
	return b.String()
}

func heloName(from string) string {
	if _, domain, ok := strings.Cut(from, "@"); ok {
		if trimmed := strings.TrimSpace(domain); trimmed != "" {
			return trimmed
		}
	}
	return "localhost"
}

func randomToken() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
