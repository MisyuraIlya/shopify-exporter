// Package report collects what a sync run actually changed in Shopify and renders
// it as a human-readable email report.
//
// The point is visibility: until this existed, a run that never started (missing
// Docker image) or a run that pushed nothing looked exactly like a healthy run.
// A run always produces a report, so silence in the inbox means the job did not
// run at all — which is itself the alert.
//
// Only REAL changes are recorded. Stock and price rows carry the before value read
// from Shopify in the same lookup the sync already performs, so "4,174 SKUs pushed"
// collapses to the handful of SKUs whose value actually moved.
package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// StepStatus is the outcome of one pipeline step.
type StepStatus string

const (
	StepOK      StepStatus = "ok"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

// priceEpsilon is the tolerance for "the price did not change". Money is pushed to
// Shopify formatted to 2 decimals, so anything under half a cent is noise.
const priceEpsilon = 0.005

// maxWarningsPerScope caps how many distinct warnings one scope contributes. A single
// repeating condition across thousands of SKUs would otherwise bury the few lines that
// need a human, and blow up the CSV. The overflow is counted instead.
const maxWarningsPerScope = 25

// Step is one stage of the pipeline (syncProducts, syncStocks, …).
type Step struct {
	Name       string
	Status     StepStatus
	StartedAt  time.Time
	FinishedAt time.Time
	Err        error
	// SkipReason explains a StepSkipped status (e.g. the SYNC_ONLY_STEPS filter).
	SkipReason string
}

// Duration is how long the step ran. Zero for a skipped step.
func (s *Step) Duration() time.Duration {
	if s == nil || s.StartedAt.IsZero() || s.FinishedAt.IsZero() {
		return 0
	}
	return s.FinishedAt.Sub(s.StartedAt)
}

// StockChange is one SKU whose Shopify on-hand quantity moved.
type StockChange struct {
	SKU string
	// Before is the on-hand quantity Shopify held before the push. BeforeKnown is
	// false when Shopify had no inventory level for the item yet (first activation).
	Before      int
	BeforeKnown bool
	After       int
}

// Delta is After-Before, or After when there was no prior level.
func (c StockChange) Delta() int {
	if !c.BeforeKnown {
		return c.After
	}
	return c.After - c.Before
}

// PriceChange is one SKU/currency pair whose Shopify price moved.
type PriceChange struct {
	SKU         string
	Currency    string
	Before      float64
	BeforeKnown bool
	After       float64
}

// ProductChange is a product created on, or failed against, Shopify.
type ProductChange struct {
	SKU    string
	Title  string
	Action string // created | failed
	Err    string
}

// Note is a warning or error attached to a scope (step or adapter).
type Note struct {
	Scope   string
	Message string
}

// Recorder is the write side of a run, consumed by the adapters and use cases.
// Every method is safe for concurrent use and safe to call on a nil Recorder value
// held in an interface-typed field only if the field is nil-checked by the caller —
// *Run itself tolerates a nil receiver.
type Recorder interface {
	// StockSeen records the outcome of pushing one SKU's quantity. It classifies
	// the SKU as changed or unchanged; only changed SKUs reach the report body.
	StockSeen(sku string, before int, beforeKnown bool, after int)
	// PriceSeen records the outcome of pushing one SKU's price in one currency.
	PriceSeen(sku, currency string, before float64, beforeKnown bool, after float64)
	// ProductCreated records a product that did not exist in Shopify before.
	ProductCreated(sku, title string)
	// ProductUpdated records a product that already existed (counted, not listed —
	// the sync re-pushes every product every run, so listing them says nothing).
	ProductUpdated(sku string)
	// ProductFailed records a product that could not be written.
	ProductFailed(sku, title string, err error)
	// Warn records a non-fatal problem worth a human's attention.
	Warn(scope, message string)
	// Incr bumps a named counter shown in the report footer.
	Incr(scope, key string, n int64)
}

// Run is the accumulated state of a single execution of a sync binary.
type Run struct {
	Job        string // binary name, e.g. sync-to-shopify
	Mode       string // "full" or the SYNC_ONLY_STEPS value
	Host       string
	Shop       string
	LogFile    string
	StartedAt  time.Time
	FinishedAt time.Time

	mu             sync.Mutex
	steps          []*Step
	stock          []StockChange
	stockUnchanged int64
	prices         []PriceChange
	priceUnchanged int64
	products       []ProductChange
	productsUpdate int64
	warnings       []Note
	// warningsByScope counts every warning offered, including ones the per-scope cap
	// suppressed, so the report can say how many there really were.
	warningsByScope    map[string]int
	suppressedWarnings int
	counters           map[string]int64
	counterOrder       []string
}

// NewRun starts a report for the given job.
func NewRun(job, mode, host, shop string, startedAt time.Time) *Run {
	if strings.TrimSpace(mode) == "" {
		mode = "full"
	}
	return &Run{
		Job:       job,
		Mode:      mode,
		Host:      host,
		Shop:      shop,
		StartedAt: startedAt,
		counters:  make(map[string]int64),
	}
}

var _ Recorder = (*Run)(nil)

// StartStep opens a step and returns it. Close it with FinishStep.
func (r *Run) StartStep(name string, at time.Time) *Step {
	if r == nil {
		return nil
	}
	step := &Step{Name: name, Status: StepOK, StartedAt: at}
	r.mu.Lock()
	r.steps = append(r.steps, step)
	r.mu.Unlock()
	return step
}

// FinishStep closes a step with its outcome.
func (r *Run) FinishStep(step *Step, at time.Time, err error) {
	if r == nil || step == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	step.FinishedAt = at
	if err != nil {
		step.Status = StepFailed
		step.Err = err
	}
}

// SkipStep records a step that never ran.
func (r *Run) SkipStep(name, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, &Step{Name: name, Status: StepSkipped, SkipReason: reason})
}

// Finish stamps the end of the run.
func (r *Run) Finish(at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.FinishedAt = at
}

// SetLogFile records the on-disk log path so the report can point at it.
func (r *Run) SetLogFile(path string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.LogFile = path
}

func (r *Run) StockSeen(sku string, before int, beforeKnown bool, after int) {
	if r == nil {
		return
	}
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if beforeKnown && before == after {
		r.stockUnchanged++
		return
	}
	r.stock = append(r.stock, StockChange{SKU: sku, Before: before, BeforeKnown: beforeKnown, After: after})
}

func (r *Run) PriceSeen(sku, currency string, before float64, beforeKnown bool, after float64) {
	if r == nil {
		return
	}
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if beforeKnown && math.Abs(before-after) < priceEpsilon {
		r.priceUnchanged++
		return
	}
	r.prices = append(r.prices, PriceChange{
		SKU:         sku,
		Currency:    strings.ToUpper(strings.TrimSpace(currency)),
		Before:      before,
		BeforeKnown: beforeKnown,
		After:       after,
	})
}

func (r *Run) ProductCreated(sku, title string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.products = append(r.products, ProductChange{
		SKU:    strings.TrimSpace(sku),
		Title:  strings.TrimSpace(title),
		Action: "created",
	})
}

func (r *Run) ProductUpdated(string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.productsUpdate++
}

func (r *Run) ProductFailed(sku, title string, err error) {
	if r == nil {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.products = append(r.products, ProductChange{
		SKU:    strings.TrimSpace(sku),
		Title:  strings.TrimSpace(title),
		Action: "failed",
		Err:    message,
	})
}

func (r *Run) Warn(scope, message string) {
	if r == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	scope = strings.TrimSpace(scope)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.warningsByScope == nil {
		r.warningsByScope = make(map[string]int)
	}
	r.warningsByScope[scope]++
	if r.warningsByScope[scope] > maxWarningsPerScope {
		// Suppressed, not lost: the total lands in the report footer as a counter.
		r.suppressedWarnings++
		return
	}
	r.warnings = append(r.warnings, Note{Scope: scope, Message: message})
}

// WarningTotals reports how many warnings were recorded per scope, including the ones
// suppressed by the per-scope cap.
func (r *Run) WarningTotals() map[string]int {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	totals := make(map[string]int, len(r.warningsByScope))
	for scope, count := range r.warningsByScope {
		totals[scope] = count
	}
	return totals
}

func (r *Run) Incr(scope, key string, n int64) {
	if r == nil || n == 0 {
		return
	}
	name := strings.TrimSpace(key)
	if scope := strings.TrimSpace(scope); scope != "" {
		name = scope + "." + name
	}
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, seen := r.counters[name]; !seen {
		r.counterOrder = append(r.counterOrder, name)
	}
	r.counters[name] += n
}

// Summary is the immutable view of a finished run, used for rendering.
type Summary struct {
	Job        string
	Mode       string
	Host       string
	Shop       string
	LogFile    string
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration

	Steps []Step

	StockChanges   []StockChange
	StockUnchanged int64
	PriceChanges   []PriceChange
	PriceUnchanged int64
	ProductsNew    []ProductChange
	ProductsFailed []ProductChange
	ProductsUpdate int64
	Warnings       []Note
	// SuppressedWarnings is how many warnings the per-scope cap dropped from Warnings.
	SuppressedWarnings int
	Counters           []Counter

	FailedSteps  int
	TotalChanges int
}

// Counter is one named tally for the report footer.
type Counter struct {
	Name  string
	Value int64
}

// Run statuses returned by Summary.Status.
const (
	StatusOK      = "ok"
	StatusWarning = "warning"
	StatusFailed  = "failed"
)

// Status classifies the run for the subject line.
func (s Summary) Status() string {
	switch {
	case s.FailedSteps > 0 || len(s.ProductsFailed) > 0:
		return StatusFailed
	case len(s.Warnings) > 0:
		return StatusWarning
	default:
		return StatusOK
	}
}

// Snapshot freezes the run into a Summary. Stock changes are sorted by the size of
// the move (largest first) so the interesting rows survive any row cap; prices and
// products sort by SKU for a stable, diffable order.
func (r *Run) Snapshot() Summary {
	if r == nil {
		return Summary{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Summary{
		Job:            r.Job,
		Mode:           r.Mode,
		Host:           r.Host,
		Shop:           r.Shop,
		LogFile:        r.LogFile,
		StartedAt:      r.StartedAt,
		FinishedAt:     r.FinishedAt,
		StockUnchanged: r.stockUnchanged,
		PriceUnchanged: r.priceUnchanged,
		ProductsUpdate: r.productsUpdate,
	}
	if !r.StartedAt.IsZero() && !r.FinishedAt.IsZero() {
		s.Duration = r.FinishedAt.Sub(r.StartedAt)
	}

	for _, step := range r.steps {
		s.Steps = append(s.Steps, *step)
		if step.Status == StepFailed {
			s.FailedSteps++
		}
	}

	s.StockChanges = append(s.StockChanges, r.stock...)
	sort.SliceStable(s.StockChanges, func(i, j int) bool {
		di, dj := abs(s.StockChanges[i].Delta()), abs(s.StockChanges[j].Delta())
		if di != dj {
			return di > dj
		}
		return s.StockChanges[i].SKU < s.StockChanges[j].SKU
	})

	s.PriceChanges = append(s.PriceChanges, r.prices...)
	sort.SliceStable(s.PriceChanges, func(i, j int) bool {
		if s.PriceChanges[i].SKU != s.PriceChanges[j].SKU {
			return s.PriceChanges[i].SKU < s.PriceChanges[j].SKU
		}
		return s.PriceChanges[i].Currency < s.PriceChanges[j].Currency
	})

	for _, p := range r.products {
		if p.Action == "failed" {
			s.ProductsFailed = append(s.ProductsFailed, p)
			continue
		}
		s.ProductsNew = append(s.ProductsNew, p)
	}
	sort.SliceStable(s.ProductsNew, func(i, j int) bool { return s.ProductsNew[i].SKU < s.ProductsNew[j].SKU })
	sort.SliceStable(s.ProductsFailed, func(i, j int) bool { return s.ProductsFailed[i].SKU < s.ProductsFailed[j].SKU })

	s.Warnings = append(s.Warnings, r.warnings...)
	s.SuppressedWarnings = r.suppressedWarnings

	for _, name := range r.counterOrder {
		s.Counters = append(s.Counters, Counter{Name: name, Value: r.counters[name]})
	}

	s.TotalChanges = len(s.StockChanges) + len(s.PriceChanges) + len(s.ProductsNew)
	return s
}

// OneLine is the compact technical summary, also used as the log line.
func (s Summary) OneLine() string {
	return fmt.Sprintf(
		"job=%s mode=%s status=%s duration=%s stock_changed=%d price_changed=%d products_new=%d products_failed=%d warnings=%d",
		s.Job,
		s.Mode,
		s.Status(),
		FormatDuration(s.Duration),
		len(s.StockChanges),
		len(s.PriceChanges),
		len(s.ProductsNew),
		len(s.ProductsFailed),
		len(s.Warnings),
	)
}

// FormatDuration renders a duration as a compact human string (1h04m, 12m30s, 8s).
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
