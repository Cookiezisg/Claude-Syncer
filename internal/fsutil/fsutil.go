// Package fsutil holds the file operations every write goes through: atomic
// replacement and a trash that keeps what we remove for a while.
package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TempPrefix starts the name of every temporary file we create. Claude
// Desktop only reads files named local_*.json, deleted_* and a few fixed
// names, and treats local_*.json.tmp as its own crash-recovery files, so our
// temporary files must never look like either.
const TempPrefix = ".claude-syncer-"

// WriteFileAtomic writes data to path through a temporary file in the same
// directory and a rename, so a reader sees the old file or the new one,
// never a partial write.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, TempPrefix+randomHex(6)+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CreateExclusive writes data to path only if nothing is there yet. It
// reports false, nil when the path already exists.
func CreateExclusive(path string, data []byte, perm fs.FileMode) (bool, error) {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, TempPrefix+randomHex(6)+".tmp")
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return false, err
	}
	defer os.Remove(tmp)
	// link(2) fails with EEXIST instead of replacing, unlike rename(2).
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CleanStaleTemps removes our temporary files older than an hour from dir,
// left behind by a run that was killed mid-write.
func CleanStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), TempPrefix) || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Trash moves removed files aside instead of deleting them, and purges them
// after a retention period.
type Trash struct {
	Dir       string
	Retention time.Duration
	Now       func() time.Time
}

// Put moves path into today's trash folder, keeping its path relative to
// base so it can be put back by hand. It returns where the file went.
func (t *Trash) Put(path, base string) (string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	day := t.now().Format("2006-01-02")
	dst := filepath.Join(t.Dir, day, rel)
	if _, err := os.Lstat(dst); err == nil {
		dst = fmt.Sprintf("%s.%s", dst, randomHex(3))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	if err := os.Rename(path, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Purge removes trash folders older than the retention period.
func (t *Trash) Purge() error {
	entries, err := os.ReadDir(t.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := t.now().Add(-t.Retention)
	var errs []error
	for _, e := range entries {
		day, err := time.ParseInLocation("2006-01-02", e.Name(), time.Local)
		if err != nil || !e.IsDir() {
			continue
		}
		if day.AddDate(0, 0, 1).Before(cutoff) {
			errs = append(errs, os.RemoveAll(filepath.Join(t.Dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}

// Size is the total size of the trash in bytes.
func (t *Trash) Size() int64 { return DirSize(t.Dir) }

func (t *Trash) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// DirSize sums the sizes of regular files under dir.
func DirSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
