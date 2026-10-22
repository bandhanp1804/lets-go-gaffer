package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
