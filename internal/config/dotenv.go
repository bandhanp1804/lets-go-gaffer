// Package config loads runtime configuration.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// LoadDotEnv reads KEY=VALUE lines from path into the process environment.
//
// Variables already set in the real environment are never overridden, so
// deployments that inject secrets directly are unaffected by a stray file. A
// missing file is not an error. Supported: blank lines, full-line # comments,
// an optional "export " prefix, and single or double quotes around a value.
// There are no inline comments, so a # inside a value (a DSN, a password) is
// kept as data.
//
// Errors name the offending line number but never echo its contents, since the
// file holds secrets.
func LoadDotEnv(path string) error {
	return parseDotEnv(path, func(key, value string) error {
		if _, set := os.LookupEnv(key); set {
			return nil
		}
		return os.Setenv(key, value)
	})
}

// parseDotEnv calls set for each KEY=VALUE line in path. A missing file is not an error.
func parseDotEnv(path string, set func(key, value string) error) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text()) // also strips a trailing \r
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("%s line %d: expected KEY=VALUE", path, n)
		}
		if err := set(key, unquote(strings.TrimSpace(value))); err != nil {
			return fmt.Errorf("%s line %d: %w", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// LiveVar is one setting that is read again whenever its file changes, for values a
// long-running process must pick up without a restart: an FPL session token lasts an
// hour, so a server that read it once at startup is stale before long.
//
// The file wins over the process environment while it has the key; without it the
// environment is used. A file that cannot be read keeps the last good value. Safe for
// concurrent use.
type LiveVar struct {
	path, key string

	mu   sync.Mutex
	mod  time.Time
	size int64
	val  string
	have bool
}

// NewLiveVar watches key in the dotenv file at path.
func NewLiveVar(path, key string) *LiveVar { return &LiveVar{path: path, key: key} }

// Get returns the current value, re-reading the file only when its modification time
// or size has changed.
func (v *LiveVar) Get() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	fi, err := os.Stat(v.path)
	switch {
	case err != nil: // no file: the environment is all there is
		v.mod, v.size, v.val, v.have = time.Time{}, 0, "", false
	case !fi.ModTime().Equal(v.mod) || fi.Size() != v.size:
		val, have, err := readKey(v.path, v.key)
		if err == nil { // a half-written file keeps the last good value
			v.val, v.have = val, have
			v.mod, v.size = fi.ModTime(), fi.Size()
		}
	}
	if v.have {
		return v.val
	}
	return os.Getenv(v.key)
}

func readKey(path, key string) (string, bool, error) {
	var val string
	var have bool
	err := parseDotEnv(path, func(k, value string) error {
		if k == key {
			val, have = value, true
		}
		return nil
	})
	return val, have, err
}

// unquote strips one matching pair of surrounding quotes.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
