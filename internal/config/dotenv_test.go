package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/config"
)

// unsetForTest guarantees a variable is absent now and restored after the test.
func unsetForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "") // registers restoration of the original value
		os.Unsetenv(k)
	}
}

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDotEnv_ParsesTheFormats(t *testing.T) {
	unsetForTest(t, "T_PLAIN", "T_EXPORT", "T_DOUBLE", "T_SINGLE", "T_HASH", "T_EQUALS", "T_EMPTY", "T_SPACED", "T_CRLF")
	path := writeEnv(t, strings.Join([]string{
		"# a comment",
		"",
		"T_PLAIN=value",
		"export T_EXPORT=exported",
		`T_DOUBLE="two words"`,
		`T_SINGLE='single quoted'`,
		"T_HASH=postgres://u:pa#ss@host/db?x=1#frag", // # inside a value is data, not a comment
		"T_EQUALS=a=b=c",
		"T_EMPTY=",
		"  T_SPACED  =  padded  ",
		"T_CRLF=windows\r",
	}, "\n"))

	if err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"T_PLAIN":  "value",
		"T_EXPORT": "exported",
		"T_DOUBLE": "two words",
		"T_SINGLE": "single quoted",
		"T_HASH":   "postgres://u:pa#ss@host/db?x=1#frag",
		"T_EQUALS": "a=b=c",
		"T_EMPTY":  "",
		"T_SPACED": "padded",
		"T_CRLF":   "windows",
	}
	for k, v := range want {
		got, ok := os.LookupEnv(k)
		if !ok || got != v {
			t.Errorf("%s = %q (set=%v), want %q", k, got, ok, v)
		}
	}
}

func TestLoadDotEnv_RealEnvironmentWins(t *testing.T) {
	// Deployments inject secrets through the real environment; a stray .env file
	// must never override them.
	t.Setenv("T_PRESET", "from-environment")
	unsetForTest(t, "T_NEW")
	path := writeEnv(t, "T_PRESET=from-file\nT_NEW=from-file\n")

	if err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("T_PRESET"); got != "from-environment" {
		t.Errorf("T_PRESET = %q, want the environment value to win", got)
	}
	if got := os.Getenv("T_NEW"); got != "from-file" {
		t.Errorf("T_NEW = %q, want from-file", got)
	}
}

func TestLoadDotEnv_MissingFileIsFine(t *testing.T) {
	// Production has no .env file; that must not be an error.
	if err := config.LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing file: %v, want nil", err)
	}
}

func TestLoadDotEnv_MalformedLineReportsItsNumberNotItsContent(t *testing.T) {
	unsetForTest(t, "T_GOOD")
	path := writeEnv(t, "T_GOOD=ok\nSUPER_SECRET_WITHOUT_EQUALS\n")

	err := config.LoadDotEnv(path)

	if err == nil {
		t.Fatal("want an error for a line without '='")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %q, want it to name line 2", err)
	}
	// The file holds secrets, so errors must never echo its contents.
	if strings.Contains(err.Error(), "SUPER_SECRET") {
		t.Errorf("err = %q leaks file content", err)
	}
}

func TestLiveVar_PicksUpAChangedFileWithoutARestart(t *testing.T) {
	unsetForTest(t, "LIVE_TOKEN")
	path := filepath.Join(t.TempDir(), ".env")
	write := func(content string, mod time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	write("OTHER=1\nLIVE_TOKEN=old\n", t0)
	v := config.NewLiveVar(path, "LIVE_TOKEN")

	if got := v.Get(); got != "old" {
		t.Fatalf("first read = %q", got)
	}
	write("OTHER=1\nLIVE_TOKEN=\"new value\"\n", t0.Add(time.Minute))
	if got := v.Get(); got != "new value" {
		t.Errorf("after the file changed = %q, want the new value (and quotes removed)", got)
	}
}

func TestLiveVar_FallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("LIVE_TOKEN", "from-env")
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	v := config.NewLiveVar(path, "LIVE_TOKEN")
	if got := v.Get(); got != "from-env" {
		t.Errorf("no file: %q", got)
	}

	if err := os.WriteFile(path, []byte("OTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := v.Get(); got != "from-env" {
		t.Errorf("file without the key: %q", got)
	}

	if err := os.WriteFile(path, []byte("LIVE_TOKEN=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := v.Get(); got != "from-file" {
		t.Errorf("the file must win while it has the key: %q", got)
	}
}

func TestLiveVar_AFileThatCannotBeParsedKeepsTheLastGoodValue(t *testing.T) {
	unsetForTest(t, "LIVE_TOKEN")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("LIVE_TOKEN=good\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := config.NewLiveVar(path, "LIVE_TOKEN")
	if v.Get() != "good" {
		t.Fatal("setup")
	}

	if err := os.WriteFile(path, []byte("this line has no equals sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := v.Get(); got != "good" {
		t.Errorf("a bad edit must not wipe the token: %q", got)
	}
}

func TestLiveVar_IsSafeForConcurrentUse(t *testing.T) {
	t.Setenv("LIVE_TOKEN", "x")
	v := config.NewLiveVar(filepath.Join(t.TempDir(), ".env"), "LIVE_TOKEN")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = v.Get()
			}
		}()
	}
	wg.Wait()
}
