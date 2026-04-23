package debugsync

import (
	"os"
	"strings"
	"sync"
)

const TraceSKUsEnv = "SYNC_TRACE_SKUS"

var (
	loadOnce sync.Once
	skuSet   map[string]struct{}

	onlySKUsLoadOnce sync.Once
	onlySKUSet       map[string]struct{}

	onlyStepsLoadOnce sync.Once
	onlyStepSet       map[string]struct{}
)

const (
	OnlySKUsEnv  = "SYNC_ONLY_SKUS"
	OnlyStepsEnv = "SYNC_ONLY_STEPS"
)

func MatchSKU(sku string) bool {
	loadOnce.Do(load)
	if len(skuSet) == 0 {
		return false
	}
	_, ok := skuSet[normalize(sku)]
	return ok
}

func load() {
	skuSet = make(map[string]struct{})
	for _, part := range splitValues(os.Getenv(TraceSKUsEnv)) {
		if normalized := normalize(part); normalized != "" {
			skuSet[normalized] = struct{}{}
		}
	}
}

func splitValues(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
}

func normalize(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeStep(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ShouldProcessSKU(sku string) bool {
	onlySKUsLoadOnce.Do(loadOnlySKUs)
	if len(onlySKUSet) == 0 {
		return true
	}
	_, ok := onlySKUSet[normalize(sku)]
	return ok
}

func HasOnlySKUFilter() bool {
	onlySKUsLoadOnce.Do(loadOnlySKUs)
	return len(onlySKUSet) > 0
}

func ShouldRunStep(step string) bool {
	onlyStepsLoadOnce.Do(loadOnlySteps)
	if len(onlyStepSet) == 0 {
		return true
	}
	_, ok := onlyStepSet[normalizeStep(step)]
	return ok
}

func HasOnlyStepFilter() bool {
	onlyStepsLoadOnce.Do(loadOnlySteps)
	return len(onlyStepSet) > 0
}

func loadOnlySKUs() {
	onlySKUSet = make(map[string]struct{})
	for _, part := range splitValues(os.Getenv(OnlySKUsEnv)) {
		if normalized := normalize(part); normalized != "" {
			onlySKUSet[normalized] = struct{}{}
		}
	}
}

func loadOnlySteps() {
	onlyStepSet = make(map[string]struct{})
	for _, part := range splitValues(os.Getenv(OnlyStepsEnv)) {
		if normalized := normalizeStep(part); normalized != "" {
			onlyStepSet[normalized] = struct{}{}
		}
	}
}
