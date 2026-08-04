// Command send-test-report delivers one sample report through the configured SMTP
// relay. It touches neither the ERP nor Shopify — it exists to prove the mail path
// works (credentials, TLS, recipients, Hebrew rendering) without waiting for a
// two-hour sync, and to re-check it after an env change.
//
//	go run ./cmd/send-test-report
package main

import (
	"errors"
	"fmt"
	"os"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/report"
	"strings"
	"time"

	_ "time/tzdata"
)

func main() {
	cfg, err := config.LoadForDailySync()
	if err != nil {
		fail(err)
	}
	if !cfg.Report.Configured() {
		fail(errors.New("report not configured: set SMTP_HOST, SMTP_FROM (or SMTP_USERNAME) and REPORT_EMAIL_TO"))
	}

	startedAt := time.Now().Add(-14 * time.Minute)
	host, _ := os.Hostname()
	run := report.NewRun("sync-stock-and-price", "syncStocks (TEST)", host, cfg.Shopify.ShopDomain, startedAt)

	step := run.StartStep("syncStocks", startedAt)
	run.FinishStep(step, startedAt.Add(13*time.Minute), nil)
	run.SkipStep("syncPrices", "skipped by SYNC_ONLY_STEPS")

	// A representative spread: a restock, a sell-down, a sell-out, a first-time item.
	run.StockSeen("CMG-28", 102, true, 247)
	run.StockSeen("DRA-1", 40, true, 12)
	run.StockSeen("CUP-2", 3, true, 0)
	run.StockSeen("CNJ-1", 0, false, 8)
	run.StockSeen("HAO-1", 15, true, 15) // unchanged: counted, not listed
	run.PriceSeen("DRA-1", "ILS", 19.80, true, 23.36)
	run.PriceSeen("DRA-1", "USD", 6.10, true, 6.50)
	run.ProductCreated("NEW-1", "פמוט פליז חדש")
	run.Warn("stock", "shopify variant not found for sku ZZ-77")
	run.Incr("stock", "pushed", 4174)
	run.Incr("stock", "skipped_missing_variant", 1)
	run.Finish(startedAt.Add(14 * time.Minute))

	summary := run.Snapshot()

	loc := time.UTC
	if zone := strings.TrimSpace(cfg.Report.Timezone); zone != "" {
		if parsed, err := time.LoadLocation(zone); err == nil {
			loc = parsed
		} else {
			fmt.Printf("[WARNING]: unknown REPORT_TIMEZONE=%q, using UTC\n", zone)
		}
	}
	opts := report.RenderOptions{MaxRows: cfg.Report.MaxRows, Location: loc}

	smtpCfg := report.SMTPConfig{
		Host:          cfg.Report.SMTP.Host,
		Port:          cfg.Report.SMTP.Port,
		Username:      cfg.Report.SMTP.Username,
		Password:      cfg.Report.SMTP.Password,
		From:          cfg.Report.SMTP.From,
		FromName:      cfg.Report.SMTP.FromName,
		To:            cfg.Report.Recipients,
		Timeout:       cfg.Report.SMTP.Timeout,
		ImplicitTLS:   cfg.Report.SMTP.ImplicitTLS,
		SkipTLSVerify: cfg.Report.SMTP.SkipTLSVerify,
	}

	fmt.Printf(
		"sending test report via %s:%d as %s -> %s\n",
		smtpCfg.Host, smtpCfg.Port, smtpCfg.From, strings.Join(smtpCfg.To, ", "),
	)
	if err := report.SendEmail(summary, smtpCfg, opts); err != nil {
		fail(err)
	}
	fmt.Println("✅ test report sent — check the inbox (subject: " + summary.Subject(opts) + ")")
}

func fail(err error) {
	fmt.Printf("❌ %v\n", err)
	os.Exit(1)
}
