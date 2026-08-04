package report

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testRun() *Run {
	return NewRun("sync-stock-and-price", "syncStocks", "instance-emanuel", "emanueljudaica.myshopify.com",
		time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
}

func TestStockSeenOnlyRecordsRealChanges(t *testing.T) {
	run := testRun()

	run.StockSeen("CMG-28", 102, true, 247) // moved
	run.StockSeen("DRA-1", 5, true, 5)      // identical -> not a change
	run.StockSeen("CUP-2", 3, true, 0)      // went out of stock
	run.StockSeen("NEW-1", 0, false, 12)    // no prior inventory level
	run.StockSeen("   ", 1, true, 9)        // blank SKU is ignored

	summary := run.Snapshot()

	if got, want := len(summary.StockChanges), 3; got != want {
		t.Fatalf("stock changes = %d, want %d (%+v)", got, want, summary.StockChanges)
	}
	if got, want := summary.StockUnchanged, int64(1); got != want {
		t.Errorf("unchanged = %d, want %d", got, want)
	}

	// Sorted by the size of the move, so the biggest swings survive the row cap.
	if got, want := summary.StockChanges[0].SKU, "CMG-28"; got != want {
		t.Errorf("first change = %q, want %q", got, want)
	}
	if got, want := summary.StockChanges[0].Delta(), 145; got != want {
		t.Errorf("CMG-28 delta = %d, want %d", got, want)
	}

	bySKU := map[string]StockChange{}
	for _, ch := range summary.StockChanges {
		bySKU[ch.SKU] = ch
	}
	if got, want := bySKU["CUP-2"].Delta(), -3; got != want {
		t.Errorf("CUP-2 delta = %d, want %d", got, want)
	}
	// An unknown before must not be reported as a jump from zero.
	if bySKU["NEW-1"].BeforeKnown {
		t.Error("NEW-1 should have BeforeKnown=false")
	}
	if got, want := bySKU["NEW-1"].Delta(), 12; got != want {
		t.Errorf("NEW-1 delta = %d, want %d", got, want)
	}
}

func TestPriceSeenIgnoresSubCentNoise(t *testing.T) {
	run := testRun()

	run.PriceSeen("DRA-1", "ils", 23.36, true, 23.36)    // identical
	run.PriceSeen("DRA-2", "ILS", 23.360, true, 23.3601) // rounding noise
	run.PriceSeen("DRA-3", "ILS", 19.80, true, 23.36)    // real change
	run.PriceSeen("DRA-4", "USD", 0, false, 6.50)        // no prior value

	summary := run.Snapshot()

	if got, want := len(summary.PriceChanges), 2; got != want {
		t.Fatalf("price changes = %d, want %d (%+v)", got, want, summary.PriceChanges)
	}
	if got, want := summary.PriceUnchanged, int64(2); got != want {
		t.Errorf("unchanged = %d, want %d", got, want)
	}
	// Currency is normalised so ILS and ils group together in the report.
	if got, want := summary.PriceChanges[0].Currency, "ILS"; got != want {
		t.Errorf("currency = %q, want %q", got, want)
	}
}

func TestStatusReflectsFailuresAndWarnings(t *testing.T) {
	t.Run("clean run is ok", func(t *testing.T) {
		run := testRun()
		step := run.StartStep("syncStocks", time.Now())
		run.FinishStep(step, time.Now(), nil)
		if got := run.Snapshot().Status(); got != "ok" {
			t.Errorf("status = %q, want ok", got)
		}
	})

	t.Run("warning only", func(t *testing.T) {
		run := testRun()
		run.Warn("stock", "shopify variant not found for sku ZZ-9")
		if got := run.Snapshot().Status(); got != "warning" {
			t.Errorf("status = %q, want warning", got)
		}
	})

	t.Run("failed step wins over warnings", func(t *testing.T) {
		run := testRun()
		run.Warn("stock", "missing variant")
		step := run.StartStep("syncStocks", time.Now())
		run.FinishStep(step, time.Now(), errors.New("shopify 502"))
		summary := run.Snapshot()
		if got := summary.Status(); got != "failed" {
			t.Errorf("status = %q, want failed", got)
		}
		if got, want := summary.FailedSteps, 1; got != want {
			t.Errorf("failed steps = %d, want %d", got, want)
		}
	})

	t.Run("failed product marks the run failed", func(t *testing.T) {
		run := testRun()
		run.ProductFailed("CNJ-1", "פמוט", errors.New("create failed"))
		if got := run.Snapshot().Status(); got != "failed" {
			t.Errorf("status = %q, want failed", got)
		}
	})
}

func TestProductsSplitCreatedFromFailed(t *testing.T) {
	run := testRun()
	run.ProductCreated("NEW-2", "Brass candlestick")
	run.ProductCreated("NEW-1", "Silver kiddush cup")
	run.ProductUpdated("OLD-1")
	run.ProductUpdated("OLD-2")
	run.ProductFailed("BAD-1", "Broken", errors.New("title is required"))

	summary := run.Snapshot()

	if got, want := len(summary.ProductsNew), 2; got != want {
		t.Fatalf("new products = %d, want %d", got, want)
	}
	if got, want := summary.ProductsNew[0].SKU, "NEW-1"; got != want {
		t.Errorf("sorted first = %q, want %q", got, want)
	}
	if got, want := len(summary.ProductsFailed), 1; got != want {
		t.Fatalf("failed products = %d, want %d", got, want)
	}
	// Re-exported products are counted, never listed.
	if got, want := summary.ProductsUpdate, int64(2); got != want {
		t.Errorf("re-exported = %d, want %d", got, want)
	}
	if got, want := summary.TotalChanges, 2; got != want {
		t.Errorf("total changes = %d, want %d (failures are not 'changes')", got, want)
	}
}

func TestCountersAreOrderedAndScoped(t *testing.T) {
	run := testRun()
	run.Incr("stock", "pushed", 4174)
	run.Incr("stock", "skipped_missing_variant", 3)
	run.Incr("stock", "pushed", 26)

	counters := run.Snapshot().Counters
	if got, want := len(counters), 2; got != want {
		t.Fatalf("counters = %d, want %d", got, want)
	}
	if got, want := counters[0].Name, "stock.pushed"; got != want {
		t.Errorf("first counter = %q, want %q", got, want)
	}
	if got, want := counters[0].Value, int64(4200); got != want {
		t.Errorf("stock.pushed = %d, want %d (increments accumulate)", got, want)
	}
}

func TestWarningsAreCappedPerScopeButStillCounted(t *testing.T) {
	// The ERP stock feed carries ~40% SKUs that are not published to Shopify. Those are
	// counted, not warned — but if anything ever does warn per SKU, one repeating
	// condition must not bury the lines that matter or blow up the CSV.
	run := testRun()
	for i := range 200 {
		run.Warn("stock", "shopify variant not found for sku BULK-"+strconv.Itoa(i))
	}
	run.Warn("products", "product skipped: empty title sku=ODD-1")

	summary := run.Snapshot()

	if got, want := len(summary.Warnings), maxWarningsPerScope+1; got != want {
		t.Errorf("warnings kept = %d, want %d (25 stock + 1 products)", got, want)
	}
	if got, want := summary.SuppressedWarnings, 175; got != want {
		t.Errorf("suppressed = %d, want %d", got, want)
	}
	// The cap is per scope, so a different scope is never crowded out.
	found := false
	for _, w := range summary.Warnings {
		if w.Scope == "products" {
			found = true
		}
	}
	if !found {
		t.Error("the products warning must survive the stock scope's flood")
	}
	if got, want := run.WarningTotals()["stock"], 200; got != want {
		t.Errorf("stock warning total = %d, want %d", got, want)
	}
	if !strings.Contains(summary.HTML(RenderOptions{}), "warnings_suppressed=175") {
		t.Error("the suppressed count must be visible in the report footer")
	}
}

func TestCSVCarriesEveryRowAndOpensInExcel(t *testing.T) {
	run := testRun()
	run.StockSeen("CMG-28", 102, true, 247)
	run.PriceSeen("DRA-1", "ILS", 19.80, true, 23.36)
	run.ProductCreated("NEW-1", "כוס קידוש")
	run.ProductFailed("BAD-1", "Broken", errors.New("boom"))
	run.Warn("stock", "variant missing")
	step := run.StartStep("syncStocks", run.StartedAt)
	run.FinishStep(step, run.StartedAt.Add(12*time.Minute), nil)

	csv := string(run.Snapshot().CSV())

	if !strings.HasPrefix(csv, "\ufeff") {
		t.Error("CSV must start with a UTF-8 BOM so Excel renders Hebrew titles")
	}
	for _, want := range []string{
		"type,sku,currency,before,after,delta,note",
		"stock,CMG-28,,102,247,+145,",
		"price,DRA-1,ILS,19.80,23.36",
		"product_created,NEW-1",
		"product_failed,BAD-1",
		"warning,",
		"step,syncStocks,,,ok,12m00s",
	} {
		if !strings.Contains(csv, want) {
			t.Errorf("CSV missing %q\n---\n%s", want, csv)
		}
	}
}

func TestHTMLShowsChangesAndEscapes(t *testing.T) {
	run := testRun()
	run.StockSeen("CMG-28", 102, true, 247)
	run.ProductCreated("XSS-1", `<script>alert(1)</script>`)
	run.Finish(run.StartedAt.Add(9 * time.Minute))

	body := run.Snapshot().HTML(RenderOptions{})

	if !strings.Contains(body, `dir="rtl"`) {
		t.Error("report body must be RTL for the Hebrew reader")
	}
	for _, want := range []string{"CMG-28", "102", "247", "+145", "9m00s"} {
		if !strings.Contains(body, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("product titles must be HTML-escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the escaped title in the body")
	}
}

func TestHTMLCapsRowsAndSaysSo(t *testing.T) {
	run := testRun()
	for i := range 40 {
		run.StockSeen(string(rune('A'+i%26))+"-1", i, true, i+1+i)
	}
	body := run.Snapshot().HTML(RenderOptions{MaxRows: 5})

	if !strings.Contains(body, "CSV") {
		t.Error("a capped table must tell the reader the full list is in the CSV")
	}
}

func TestCleanRunSaysNothingNeededUpdating(t *testing.T) {
	run := testRun()
	run.StockSeen("DRA-1", 5, true, 5)
	step := run.StartStep("syncStocks", run.StartedAt)
	run.FinishStep(step, run.StartedAt.Add(time.Minute), nil)
	run.Finish(run.StartedAt.Add(time.Minute))

	summary := run.Snapshot()
	if got, want := summary.TotalChanges, 0; got != want {
		t.Fatalf("total changes = %d, want %d", got, want)
	}
	if !strings.Contains(summary.HTML(RenderOptions{}), "לא נדרש שום עדכון") {
		t.Error("a no-op run should say explicitly that nothing needed updating")
	}
	if !strings.Contains(summary.Subject(RenderOptions{}), "✅") {
		t.Errorf("clean subject should carry the ok marker, got %q", summary.Subject(RenderOptions{}))
	}
}

func TestSubjectCarriesStatusAndCount(t *testing.T) {
	run := testRun()
	run.StockSeen("CMG-28", 102, true, 247)
	run.Warn("stock", "variant missing")

	subject := run.Snapshot().Subject(RenderOptions{Location: time.UTC})

	for _, want := range []string{"⚠️", "syncStocks", "1"} {
		if !strings.Contains(subject, want) {
			t.Errorf("subject %q missing %q", subject, want)
		}
	}
}

func TestBuildMessageIsWellFormedMIME(t *testing.T) {
	run := testRun()
	run.StockSeen("CMG-28", 102, true, 247)
	run.Finish(run.StartedAt.Add(time.Minute))
	summary := run.Snapshot()

	cfg := SMTPConfig{
		Host:     "smtp.office365.com",
		Port:     587,
		From:     "orders@emanueljudaica.co.il",
		FromName: "Shopify Sync",
		To:       []string{"ilya.mi@digi-trade.io", "assaf@digi-trade.io"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	raw := string(buildMessage(summary, cfg, RenderOptions{}))
	headers, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatal("message has no header/body separator")
	}

	if !strings.Contains(headers, "To: ilya.mi@digi-trade.io, assaf@digi-trade.io") {
		t.Errorf("both recipients must be in the To header:\n%s", headers)
	}
	// Hebrew and emoji cannot travel as raw bytes in a header. RFC 2047 allows
	// either case for the encoding token; Go emits the lowercase form.
	if !strings.Contains(strings.ToUpper(headers), "SUBJECT: =?UTF-8?B?") {
		t.Errorf("subject must be RFC 2047 encoded:\n%s", headers)
	}
	if !strings.Contains(headers, "Auto-Submitted: auto-generated") {
		t.Error("report mail should be marked auto-generated")
	}
	if !strings.Contains(headers, "X-Shopify-Sync-Status: ok") {
		t.Error("status header missing, filtering on it is the point")
	}
	if !strings.Contains(body, "Content-Type: text/csv") {
		t.Error("CSV attachment part missing")
	}
	if !strings.Contains(body, `filename="sync-stock-and-price-20260804-120000-changes.csv"`) {
		t.Errorf("attachment filename should be stamped with the run start:\n%s", body)
	}

	// Every base64 line must be within the MIME limit and decode cleanly.
	htmlPart, _, _ := strings.Cut(body, "\r\n--shopify-report-")
	_, encoded, _ := strings.Cut(htmlPart, "\r\n\r\n")
	for _, line := range strings.Split(strings.TrimSpace(encoded), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line exceeds 76 chars: %d", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(encoded), "\r\n", ""))
	if err != nil {
		t.Fatalf("HTML part does not decode: %v", err)
	}
	if !strings.Contains(string(decoded), "CMG-28") {
		t.Error("decoded HTML part should contain the change")
	}
}

func TestValidateRejectsIncompleteSMTP(t *testing.T) {
	cases := map[string]SMTPConfig{
		"no host":         {Port: 587, From: "a@b.co", To: []string{"c@d.co"}},
		"no port":         {Host: "smtp", From: "a@b.co", To: []string{"c@d.co"}},
		"no from":         {Host: "smtp", Port: 587, To: []string{"c@d.co"}},
		"no recipients":   {Host: "smtp", Port: 587, From: "a@b.co"},
		"blank recipient": {Host: "smtp", Port: 587, From: "a@b.co", To: []string{"  "}},
	}
	for name, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want error", name)
		}
	}
}

func TestRecordingIsConcurrencySafe(t *testing.T) {
	// The stock sync pushes batches from four goroutines at once.
	run := testRun()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run.StockSeen("SKU-"+string(rune('A'+i%26)), i, true, i+1)
			run.PriceSeen("SKU-"+string(rune('A'+i%26)), "ILS", float64(i), true, float64(i)+1)
			run.Incr("stock", "pushed", 1)
			run.Warn("stock", "note")
		}(i)
	}
	wg.Wait()

	summary := run.Snapshot()
	if got, want := len(summary.StockChanges), 50; got != want {
		t.Errorf("stock changes = %d, want %d", got, want)
	}
	// 50 warnings in one scope, capped at 25 kept + 25 counted as suppressed.
	if got, want := len(summary.Warnings), maxWarningsPerScope; got != want {
		t.Errorf("warnings kept = %d, want %d", got, want)
	}
	if got, want := summary.SuppressedWarnings, 50-maxWarningsPerScope; got != want {
		t.Errorf("warnings suppressed = %d, want %d", got, want)
	}
	if got, want := run.WarningTotals()["stock"], 50; got != want {
		t.Errorf("warning total = %d, want %d (no warning is lost from the tally)", got, want)
	}
	if got, want := summary.Counters[0].Value, int64(50); got != want {
		t.Errorf("counter = %d, want %d", got, want)
	}
}

func TestNilRunToleratesEveryCall(t *testing.T) {
	// A run with reporting switched off passes a nil *Run around; nothing may panic.
	var run *Run
	run.StockSeen("A-1", 1, true, 2)
	run.PriceSeen("A-1", "ILS", 1, true, 2)
	run.ProductCreated("A-1", "t")
	run.ProductUpdated("A-1")
	run.ProductFailed("A-1", "t", errors.New("x"))
	run.Warn("s", "m")
	run.Incr("s", "k", 1)
	run.SkipStep("syncStocks", "filtered")
	run.FinishStep(run.StartStep("x", time.Now()), time.Now(), nil)
	run.SetLogFile("/tmp/x.log")
	run.Finish(time.Now())
	if got := run.Snapshot().TotalChanges; got != 0 {
		t.Errorf("nil run total changes = %d, want 0", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                  "-",
		8 * time.Second:                    "8s",
		12*time.Minute + 30*time.Second:    "12m30s",
		time.Hour + 4*time.Minute:          "1h04m",
		2*time.Hour + 15*time.Minute + 900: "2h15m",
	}
	for in, want := range cases {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%s) = %q, want %q", in, got, want)
		}
	}
}
