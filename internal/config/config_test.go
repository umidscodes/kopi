package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}) < 0
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope", "kopi.yml"))
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if c.Port != 47800 {
		t.Errorf("Port = %d, want 47800", c.Port)
	}
	if c.DeviceName == "" {
		t.Error("DeviceName is empty, want hostname default")
	}
	if !isHex32(c.DeviceID) {
		t.Errorf("DeviceID = %q, want 32 lowercase hex chars", c.DeviceID)
	}
	if c.Passphrase != "" || c.IncludeSensitive {
		t.Errorf("Passphrase/IncludeSensitive = %q/%v, want empty/false", c.Passphrase, c.IncludeSensitive)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	want := Config{
		DeviceID:         "0123456789abcdef0123456789abcdef",
		DeviceName:       "my-laptop",
		Passphrase:       "correct horse battery staple",
		IncludeSensitive: true,
		Port:             51234,
	}
	path := filepath.Join(t.TempDir(), "nested", "kopi.yml")
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestLoadFillsZeroFieldsFromDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kopi.yml")
	if err := os.WriteFile(path, []byte("passphrase: abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Passphrase != "abc" {
		t.Errorf("Passphrase = %q, want abc", c.Passphrase)
	}
	if c.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", c.Port, DefaultPort)
	}
	if c.DeviceName == "" {
		t.Error("DeviceName not filled from default")
	}
	if !isHex32(c.DeviceID) {
		t.Errorf("DeviceID = %q, want generated 32-char hex", c.DeviceID)
	}
}

func TestLoadInvalidYAMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kopi.yml")
	if err := os.WriteFile(path, []byte("port: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load on invalid YAML returned nil error, want non-nil")
	}
}

func TestValidate(t *testing.T) {
	base := Default()
	cases := []struct {
		name string
		mut  func(*Config)
		ok   bool
	}{
		{"default", func(*Config) {}, true},
		{"port zero", func(c *Config) { c.Port = 0 }, false},
		{"port too high", func(c *Config) { c.Port = 70000 }, false},
		{"empty device name", func(c *Config) { c.DeviceName = "" }, false},
		{"empty device id", func(c *Config) { c.DeviceID = "" }, false},
	}
	for _, tc := range cases {
		c := base
		tc.mut(&c)
		err := c.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: Validate = %v, want nil", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: Validate = nil, want error", tc.name)
		}
	}
}

func TestPassphraseNeverLeaksInFormatting(t *testing.T) {
	c := Default()
	c.Passphrase = "hunter2-secret"
	forms := map[string]string{
		"%v":    fmt.Sprintf("%v", c),
		"%+v":   fmt.Sprintf("%+v", c),
		"%#v":   fmt.Sprintf("%#v", c),
		"%s":    fmt.Sprintf("%s", c),
		"Str":   c.String(),
		"goStr": c.GoString(),
	}
	for name, s := range forms {
		if strings.Contains(s, "hunter2-secret") {
			t.Errorf("passphrase leaked via %s: %s", name, s)
		}
		if !strings.Contains(s, "[set]") {
			t.Errorf("%s output lacks redaction marker [set]: %s", name, s)
		}
	}
}

func TestSaveLoadKeepsTrickyPassphrasesExactly(t *testing.T) {
	// YAML reads some bare scalars as booleans, numbers or null. A passphrase must
	// come back byte-for-byte whatever it looks like.
	for _, p := range []string{
		"no", "yes", "null", "~", "0123", "1e3", "true",
		"  leading and trailing  ", "has # hash", "key: value", "- dash", "multi\nline",
		"quote\"s and 'single'", "tab\there", "émoji 🔑",
	} {
		path := filepath.Join(t.TempDir(), "kopi.yml")
		c := Default()
		c.Passphrase = p
		if err := Save(path, c); err != nil {
			t.Fatalf("Save(%q): %v", p, err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", p, err)
		}
		if got.Passphrase != p {
			t.Errorf("passphrase %q came back as %q", p, got.Passphrase)
		}
	}
}

func TestPassphraseNeverLeaksViaPointer(t *testing.T) {
	c := Default()
	c.Passphrase = "hunter2-secret"
	for _, s := range []string{fmt.Sprintf("%v", &c), fmt.Sprintf("%+v", &c), fmt.Sprintf("%#v", &c)} {
		if strings.Contains(s, "hunter2-secret") {
			t.Errorf("passphrase leaked via pointer formatting: %s", s)
		}
	}
}

func TestDefaultDeviceIDsDiffer(t *testing.T) {
	if a, b := Default().DeviceID, Default().DeviceID; a == b {
		t.Errorf("two Default() calls share DeviceID %q", a)
	}
}
