// Package logx is a small size-capped log file.
package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxSize is the size at which the log is rotated. One previous file is
// kept, so the log never uses more than twice this.
const MaxSize = 1 << 20

// Logger appends timestamped lines to a file.
type Logger struct {
	path string
}

// Open returns a logger writing to path, rotating it first if it is full.
func Open(path string) *Logger {
	os.MkdirAll(filepath.Dir(path), 0o755)
	if info, err := os.Stat(path); err == nil && info.Size() > MaxSize {
		os.Rename(path, path+".1")
	}
	return &Logger{path: path}
}

// Printf appends one line.
func (l *Logger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}
