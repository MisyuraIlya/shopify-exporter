package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type fileLogger struct {
	file *os.File
	mu   sync.Mutex
}

func newFileLogger(dir, jobName string) (LoggerService, string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}

	filename := fmt.Sprintf("%s-%s.log", fileNamePrefix(jobName), time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(dir, filename)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", err
	}

	return &fileLogger{file: file}, path, nil
}

func fileNamePrefix(jobName string) string {
	jobName = strings.TrimSpace(jobName)
	if jobName == "" {
		return "shopify-exporter"
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		" ", "-",
		"_", "-",
	)
	jobName = replacer.Replace(strings.ToLower(jobName))
	jobName = strings.Trim(jobName, "-.")
	if jobName == "" {
		return "shopify-exporter"
	}
	return jobName
}

func (f *fileLogger) Log(value string) {
	f.write("INFO", value)
}

func (f *fileLogger) LogError(value string, err error) {
	msg := value
	if err != nil {
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%s\nerror: %s", msg, err.Error())
		}
	}
	f.write("ERROR", msg)
}

func (f *fileLogger) LogWarning(value string) {
	f.write("WARNING", value)
}

func (f *fileLogger) LogSuccess(value string) {
	f.write("SUCCESS", value)
}

func (f *fileLogger) write(level, value string) {
	if f == nil || f.file == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = fmt.Fprintln(f.file, formatFileMessage(level, value))
}

func formatFileMessage(level, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		v = "-"
	}
	return fmt.Sprintf("%s [%s] %s", time.Now().UTC().Format(time.RFC3339), level, v)
}
