// Package reporting wires the report collector to a sync binary: it builds the run,
// times each step, and delivers the email at the end. Both sync entrypoints use it
// so the report behaves identically whichever job cron fires.
package reporting

import (
	"fmt"
	"os"
	"shopify-exporter/internal/config"
	"shopify-exporter/internal/logging"
	"shopify-exporter/internal/report"
	"strings"
	"time"

	// tzdata is embedded so REPORT_TIMEZONE (Asia/Jerusalem) resolves inside the
	// scratch alpine image, which ships no system zoneinfo.
	_ "time/tzdata"
)

// Reporter accumulates a run and sends its report.
type Reporter struct {
	run    *report.Run
	logger logging.LoggerService
	cfg    config.ReportConfig
	opts   report.RenderOptions
	// send is false when the report cannot be delivered; the run is still collected
	// and its summary still logged.
	send bool
	sent bool
}

// Start opens a report for the given job. mode is the SYNC_ONLY_STEPS value, or
// empty for a full run. It never fails: a report that cannot be delivered degrades
// to a logged summary.
func Start(job string, cfg *config.DailyConfig, logger logging.LoggerService, startedAt time.Time) *Reporter {
	mode := strings.TrimSpace(os.Getenv("SYNC_ONLY_STEPS"))
	if mode == "" {
		mode = "full"
	}

	host, _ := os.Hostname()
	shop := ""
	reportCfg := config.ReportConfig{}
	if cfg != nil {
		shop = cfg.Shopify.ShopDomain
		reportCfg = cfg.Report
	}

	r := &Reporter{
		run:    report.NewRun(job, mode, host, shop, startedAt),
		logger: logger,
		cfg:    reportCfg,
		opts: report.RenderOptions{
			MaxRows:  reportCfg.MaxRows,
			Location: resolveLocation(reportCfg.Timezone, logger),
		},
	}

	switch {
	case !reportCfg.Enabled:
		logWarning(logger, "email report disabled by REPORT_EMAIL_ENABLED")
	case !reportCfg.Configured():
		logWarning(logger, "email report skipped: set SMTP_HOST, SMTP_FROM and REPORT_EMAIL_TO to enable it")
	default:
		r.send = true
		logInfo(logger, fmt.Sprintf("email report enabled recipients=%s", strings.Join(reportCfg.Recipients, ",")))
	}

	return r
}

// Recorder is what the adapters and use cases write their changes into.
func (r *Reporter) Recorder() report.Recorder {
	if r == nil {
		return nil
	}
	return r.run
}

// SetLogFile records the on-disk log path so the report can point at it.
func (r *Reporter) SetLogFile(path string) {
	if r == nil {
		return
	}
	r.run.SetLogFile(path)
}

// Step times one pipeline step. Call the returned function with the step's error
// (nil on success) when it finishes.
func (r *Reporter) Step(name string) func(err error) {
	if r == nil {
		return func(error) {}
	}
	step := r.run.StartStep(name, time.Now())
	return func(err error) {
		r.run.FinishStep(step, time.Now(), err)
	}
}

// Skip records a step that never ran.
func (r *Reporter) Skip(name, reason string) {
	if r == nil {
		return
	}
	r.run.SkipStep(name, reason)
}

// Send closes the run, logs the one-line summary, and delivers the email. It is
// idempotent and never returns an error: a failed send is logged, because losing
// the report must not fail the sync that produced it.
func (r *Reporter) Send() {
	if r == nil || r.sent {
		return
	}
	r.sent = true
	r.run.Finish(time.Now())
	summary := r.run.Snapshot()

	logInfo(r.logger, "report "+summary.OneLine())

	if !r.send {
		return
	}

	smtpCfg := report.SMTPConfig{
		Host:          r.cfg.SMTP.Host,
		Port:          r.cfg.SMTP.Port,
		Username:      r.cfg.SMTP.Username,
		Password:      r.cfg.SMTP.Password,
		From:          r.cfg.SMTP.From,
		FromName:      r.cfg.SMTP.FromName,
		To:            r.cfg.Recipients,
		Timeout:       r.cfg.SMTP.Timeout,
		ImplicitTLS:   r.cfg.SMTP.ImplicitTLS,
		SkipTLSVerify: r.cfg.SMTP.SkipTLSVerify,
	}

	if err := report.SendEmail(summary, smtpCfg, r.opts); err != nil {
		logError(r.logger, "email report send failed", err)
		return
	}
	logSuccess(r.logger, fmt.Sprintf(
		"email report sent recipients=%s changes=%d",
		strings.Join(r.cfg.Recipients, ","),
		summary.TotalChanges,
	))
}

func resolveLocation(name string, logger logging.LoggerService) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		logWarning(logger, fmt.Sprintf("unknown REPORT_TIMEZONE=%q, using UTC", name))
		return time.UTC
	}
	return loc
}

func logInfo(logger logging.LoggerService, message string) {
	if logger != nil {
		logger.Log(message)
	}
}

func logWarning(logger logging.LoggerService, message string) {
	if logger != nil {
		logger.LogWarning(message)
	}
}

func logSuccess(logger logging.LoggerService, message string) {
	if logger != nil {
		logger.LogSuccess(message)
	}
}

func logError(logger logging.LoggerService, message string, err error) {
	if logger != nil {
		logger.LogError(message, err)
	}
}
