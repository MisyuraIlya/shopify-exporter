package report

import (
	"encoding/csv"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxRows caps how many rows of each table are inlined in the HTML body.
// The CSV attachment always carries every row.
const DefaultMaxRows = 50

// RenderOptions controls report rendering.
type RenderOptions struct {
	// MaxRows caps inline table rows per section (0 -> DefaultMaxRows).
	MaxRows int
	// Location renders timestamps in the reader's timezone (nil -> UTC).
	Location *time.Location
}

func (o RenderOptions) maxRows() int {
	if o.MaxRows <= 0 {
		return DefaultMaxRows
	}
	return o.MaxRows
}

func (o RenderOptions) location() *time.Location {
	if o.Location == nil {
		return time.UTC
	}
	return o.Location
}

// Subject is the email subject line: status marker, job mode, and the headline
// number so the inbox list alone answers "did anything change?".
func (s Summary) Subject(opts RenderOptions) string {
	marker := "✅"
	statusHe := "תקין"
	switch s.Status() {
	case "failed":
		marker = "❌"
		statusHe = "כשל"
	case "warning":
		marker = "⚠️"
		statusHe = "אזהרות"
	}
	when := s.StartedAt.In(opts.location()).Format("02/01 15:04")
	return fmt.Sprintf(
		"%s Shopify sync (%s) — %s — %d שינויים — %s",
		marker,
		s.Mode,
		statusHe,
		s.TotalChanges,
		when,
	)
}

// HTML renders the report body. Hebrew/RTL for the business reader, with an English
// technical block at the bottom for grepping and for pasting into a ticket.
func (s Summary) HTML(opts RenderOptions) string {
	loc := opts.location()
	max := opts.maxRows()

	statusColor := "#137333"
	statusHe := "הסתיים בהצלחה"
	switch s.Status() {
	case "failed":
		statusColor = "#c5221f"
		statusHe = "הסתיים עם כשלים"
	case "warning":
		statusColor = "#b06000"
		statusHe = "הסתיים עם אזהרות"
	}

	var b strings.Builder
	b.WriteString(`<div dir="rtl" style="font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#202124;line-height:1.6;max-width:860px">`)

	// Header.
	b.WriteString(`<h2 style="margin:0 0 4px;font-size:20px">דוח סנכרון Shopify</h2>`)
	b.WriteString(fmt.Sprintf(
		`<div style="color:%s;font-weight:bold;margin-bottom:12px">%s</div>`,
		statusColor,
		html.EscapeString(statusHe),
	))

	b.WriteString(`<table style="border-collapse:collapse;margin-bottom:18px">`)
	metaRow(&b, "סוג ריצה", ltr(s.Mode))
	metaRow(&b, "התחלה", ltr(formatTime(s.StartedAt, loc)))
	metaRow(&b, "סיום", ltr(formatTime(s.FinishedAt, loc)))
	metaRow(&b, "משך", ltr(FormatDuration(s.Duration)))
	if s.Shop != "" {
		metaRow(&b, "חנות", ltr(s.Shop))
	}
	b.WriteString(`</table>`)

	// Headline.
	b.WriteString(`<table style="border-collapse:collapse;margin-bottom:22px">`)
	b.WriteString(`<tr>`)
	headlineCell(&b, "שינויי מלאי", len(s.StockChanges))
	headlineCell(&b, "שינויי מחיר", len(s.PriceChanges))
	headlineCell(&b, "מוצרים חדשים", len(s.ProductsNew))
	headlineCell(&b, "כשלים", len(s.ProductsFailed))
	b.WriteString(`</tr></table>`)

	if s.TotalChanges == 0 && s.Status() == "ok" {
		b.WriteString(`<p style="background:#e6f4ea;padding:12px;border-radius:4px;margin:0 0 22px">` +
			`הסנכרון רץ כשורה ולא נדרש שום עדכון — כל הנתונים ב-Shopify זהים לחשבשבת.</p>`)
	}

	// Steps.
	if len(s.Steps) > 0 {
		sectionTitle(&b, "שלבי הריצה")
		b.WriteString(tableOpen())
		b.WriteString(headerRow("שלב", "מצב", "משך", "הערה"))
		for _, step := range s.Steps {
			state, color := stepLabel(step.Status)
			note := ""
			switch {
			case step.Err != nil:
				note = step.Err.Error()
			case step.SkipReason != "":
				note = step.SkipReason
			}
			b.WriteString(`<tr>`)
			cell(&b, ltr(step.Name), "")
			cell(&b, html.EscapeString(state), "color:"+color+";font-weight:bold")
			cell(&b, ltr(FormatDuration(step.Duration())), "")
			cell(&b, ltr(truncate(note, 200)), "color:#5f6368")
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table>`)
	}

	// Stock changes.
	if len(s.StockChanges) > 0 {
		sectionTitle(&b, fmt.Sprintf("שינויי מלאי (%d)", len(s.StockChanges)))
		b.WriteString(tableOpen())
		b.WriteString(headerRow("מק\"ט", "לפני", "אחרי", "שינוי"))
		for i, ch := range s.StockChanges {
			if i >= max {
				break
			}
			delta := ch.Delta()
			color := "#137333"
			if delta < 0 {
				color = "#c5221f"
			}
			b.WriteString(`<tr>`)
			cell(&b, ltr(ch.SKU), "font-weight:bold")
			cell(&b, ltr(stockBefore(ch)), "")
			cell(&b, ltr(strconv.Itoa(ch.After)), "")
			cell(&b, ltr(withSign(delta)), "color:"+color+";font-weight:bold")
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table>`)
		writeTruncationNote(&b, len(s.StockChanges), max)
	}

	// Price changes.
	if len(s.PriceChanges) > 0 {
		sectionTitle(&b, fmt.Sprintf("שינויי מחיר (%d)", len(s.PriceChanges)))
		b.WriteString(tableOpen())
		b.WriteString(headerRow("מק\"ט", "מטבע", "לפני", "אחרי"))
		for i, ch := range s.PriceChanges {
			if i >= max {
				break
			}
			b.WriteString(`<tr>`)
			cell(&b, ltr(ch.SKU), "font-weight:bold")
			cell(&b, ltr(ch.Currency), "")
			cell(&b, ltr(priceBefore(ch)), "")
			cell(&b, ltr(formatMoney(ch.After)), "font-weight:bold")
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table>`)
		writeTruncationNote(&b, len(s.PriceChanges), max)
	}

	// New products.
	if len(s.ProductsNew) > 0 {
		sectionTitle(&b, fmt.Sprintf("מוצרים חדשים שנוספו (%d)", len(s.ProductsNew)))
		b.WriteString(tableOpen())
		b.WriteString(headerRow("מק\"ט", "שם"))
		for i, p := range s.ProductsNew {
			if i >= max {
				break
			}
			b.WriteString(`<tr>`)
			cell(&b, ltr(p.SKU), "font-weight:bold")
			cell(&b, html.EscapeString(p.Title), "")
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table>`)
		writeTruncationNote(&b, len(s.ProductsNew), max)
	}

	// Failures.
	if len(s.ProductsFailed) > 0 {
		sectionTitle(&b, fmt.Sprintf("מוצרים שנכשלו (%d)", len(s.ProductsFailed)))
		b.WriteString(tableOpen())
		b.WriteString(headerRow("מק\"ט", "שם", "שגיאה"))
		for i, p := range s.ProductsFailed {
			if i >= max {
				break
			}
			b.WriteString(`<tr>`)
			cell(&b, ltr(p.SKU), "font-weight:bold")
			cell(&b, html.EscapeString(p.Title), "")
			cell(&b, ltr(truncate(p.Err, 200)), "color:#c5221f")
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</table>`)
		writeTruncationNote(&b, len(s.ProductsFailed), max)
	}

	// Warnings.
	if len(s.Warnings) > 0 {
		sectionTitle(&b, fmt.Sprintf("אזהרות (%d)", len(s.Warnings)))
		b.WriteString(`<ul style="margin:0 0 18px;padding-inline-start:20px;color:#5f6368">`)
		for i, w := range s.Warnings {
			if i >= max {
				break
			}
			label := w.Message
			if w.Scope != "" {
				label = w.Scope + ": " + label
			}
			b.WriteString(`<li>` + ltr(truncate(label, 240)) + `</li>`)
		}
		b.WriteString(`</ul>`)
		writeTruncationNote(&b, len(s.Warnings), max)
	}

	// Technical footer, LTR.
	b.WriteString(`<div dir="ltr" style="margin-top:24px;padding-top:14px;border-top:1px solid #dadce0;` +
		`font-family:'SFMono-Regular',Consolas,monospace;font-size:12px;color:#5f6368;white-space:pre-wrap">`)
	b.WriteString(html.EscapeString(s.OneLine()))
	if s.StockUnchanged > 0 || s.PriceUnchanged > 0 || s.ProductsUpdate > 0 {
		b.WriteString(html.EscapeString(fmt.Sprintf(
			"\nunchanged: stock=%d price=%d products_reexported=%d",
			s.StockUnchanged,
			s.PriceUnchanged,
			s.ProductsUpdate,
		)))
	}
	for _, c := range s.Counters {
		b.WriteString(html.EscapeString(fmt.Sprintf("\n%s=%d", c.Name, c.Value)))
	}
	if s.Host != "" {
		b.WriteString(html.EscapeString("\nhost=" + s.Host))
	}
	if s.LogFile != "" {
		b.WriteString(html.EscapeString("\nlog=" + s.LogFile))
	}
	b.WriteString(`</div></div>`)

	return b.String()
}

// CSV renders every recorded change as a flat CSV — the full list the HTML caps.
func (s Summary) CSV() []byte {
	var buf strings.Builder
	// BOM so Excel opens the UTF-8 Hebrew titles correctly.
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"type", "sku", "currency", "before", "after", "delta", "note"})

	for _, ch := range s.StockChanges {
		_ = w.Write([]string{
			"stock",
			ch.SKU,
			"",
			stockBefore(ch),
			strconv.Itoa(ch.After),
			withSign(ch.Delta()),
			"",
		})
	}
	for _, ch := range s.PriceChanges {
		_ = w.Write([]string{
			"price",
			ch.SKU,
			ch.Currency,
			priceBefore(ch),
			formatMoney(ch.After),
			"",
			"",
		})
	}
	for _, p := range s.ProductsNew {
		_ = w.Write([]string{"product_created", p.SKU, "", "", "", "", p.Title})
	}
	for _, p := range s.ProductsFailed {
		_ = w.Write([]string{"product_failed", p.SKU, "", "", "", "", strings.TrimSpace(p.Title + " | " + p.Err)})
	}
	for _, warning := range s.Warnings {
		_ = w.Write([]string{"warning", "", "", "", "", "", strings.TrimSpace(warning.Scope + ": " + warning.Message)})
	}
	for _, step := range s.Steps {
		note := ""
		if step.Err != nil {
			note = step.Err.Error()
		} else if step.SkipReason != "" {
			note = step.SkipReason
		}
		_ = w.Write([]string{
			"step",
			step.Name,
			"",
			"",
			string(step.Status),
			FormatDuration(step.Duration()),
			note,
		})
	}
	w.Flush()
	return []byte(buf.String())
}

// CSVFilename is the attachment name, stamped with the run start.
func (s Summary) CSVFilename() string {
	stamp := s.StartedAt.UTC().Format("20060102-150405")
	job := s.Job
	if job == "" {
		job = "shopify-sync"
	}
	return fmt.Sprintf("%s-%s-changes.csv", job, stamp)
}

func stockBefore(ch StockChange) string {
	if !ch.BeforeKnown {
		return "—"
	}
	return strconv.Itoa(ch.Before)
}

func priceBefore(ch PriceChange) string {
	if !ch.BeforeKnown {
		return "—"
	}
	return formatMoney(ch.Before)
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func withSign(delta int) string {
	if delta > 0 {
		return "+" + strconv.Itoa(delta)
	}
	return strconv.Itoa(delta)
}

func formatTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(loc).Format("2006-01-02 15:04:05")
}

func stepLabel(status StepStatus) (string, string) {
	switch status {
	case StepFailed:
		return "כשל", "#c5221f"
	case StepSkipped:
		return "לא רץ", "#5f6368"
	default:
		return "תקין", "#137333"
	}
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// ltr wraps a value so SKUs, numbers and paths read left-to-right inside the RTL body.
func ltr(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return `<span dir="ltr" style="unicode-bidi:isolate">` + html.EscapeString(v) + `</span>`
}

func tableOpen() string {
	return `<table style="border-collapse:collapse;width:100%;margin:0 0 6px;font-size:13px">`
}

func headerRow(labels ...string) string {
	var b strings.Builder
	b.WriteString(`<tr style="background:#f1f3f4">`)
	for _, label := range labels {
		b.WriteString(`<th style="text-align:right;padding:6px 10px;border:1px solid #dadce0;font-weight:bold">`)
		b.WriteString(html.EscapeString(label))
		b.WriteString(`</th>`)
	}
	b.WriteString(`</tr>`)
	return b.String()
}

func cell(b *strings.Builder, value, extraStyle string) {
	style := "padding:5px 10px;border:1px solid #dadce0;text-align:right"
	if extraStyle != "" {
		style += ";" + extraStyle
	}
	b.WriteString(`<td style="` + style + `">` + value + `</td>`)
}

func metaRow(b *strings.Builder, label, value string) {
	b.WriteString(`<tr><td style="padding:2px 0 2px 14px;color:#5f6368">` + html.EscapeString(label) + `</td>`)
	b.WriteString(`<td style="padding:2px 0;font-weight:bold">` + value + `</td></tr>`)
}

func headlineCell(b *strings.Builder, label string, value int) {
	b.WriteString(`<td style="padding:10px 18px;border:1px solid #dadce0;text-align:center;min-width:96px">`)
	b.WriteString(`<div style="font-size:22px;font-weight:bold">` + strconv.Itoa(value) + `</div>`)
	b.WriteString(`<div style="font-size:12px;color:#5f6368">` + html.EscapeString(label) + `</div>`)
	b.WriteString(`</td>`)
}

func sectionTitle(b *strings.Builder, title string) {
	b.WriteString(`<h3 style="margin:20px 0 8px;font-size:15px">` + html.EscapeString(title) + `</h3>`)
}

func writeTruncationNote(b *strings.Builder, total, max int) {
	if total <= max {
		return
	}
	b.WriteString(fmt.Sprintf(
		`<div style="font-size:12px;color:#5f6368;margin:0 0 16px">מוצגות %d מתוך %d שורות — הרשימה המלאה בקובץ ה-CSV המצורף.</div>`,
		max,
		total,
	))
}
