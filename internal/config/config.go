// Package config loads and saves Kopi's YAML configuration file.
//
// The passphrase is confidential: Config implements fmt.Stringer and
// fmt.GoStringer so fmt %v, %s and %#v of a value print "[set]"/"[not set]"
// instead of the secret.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultPort is the TCP port Kopi listens on when the config doesn't set one.
const DefaultPort = 47800

// Config holds everything the app needs to run.
type Config struct {
	DeviceID         string `yaml:"device_id"`
	DeviceName       string `yaml:"device_name"`
	Passphrase       string `yaml:"passphrase"`
	IncludeSensitive bool   `yaml:"include_sensitive"`
	Port             int    `yaml:"port"`
}

// Default returns a config with a fresh random DeviceID, the hostname as
// device name and the default port. Passphrase is empty.
func Default() Config {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "kopi-device"
	}
	return Config{
		DeviceID:   newDeviceID(),
		DeviceName: name,
		Port:       DefaultPort,
	}
}

// newDeviceID returns 32 lowercase hex characters from crypto/rand.
func newDeviceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failing system CSPRNG means the platform is broken; there is no
		// sensible fallback that keeps device identity unique.
		panic("config: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// Path returns the location of the config file inside the OS user config dir.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kopi", "kopi.yml"), nil
}

// Load reads the YAML file at path. A missing file yields Default(). Fields
// missing or zero in the file are filled from Default() for DeviceID,
// DeviceName and Port; Passphrase and IncludeSensitive are taken as-is.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	d := Default()
	if c.DeviceID == "" {
		c.DeviceID = d.DeviceID
	}
	if c.DeviceName == "" {
		c.DeviceName = d.DeviceName
	}
	if c.Port == 0 {
		c.Port = d.Port
	}
	return c, nil
}

// Save writes c to path as YAML. The parent directory is created with 0700 if
// missing; the file is written with 0600 and moved into place atomically via
// a temp file in the same directory.
func Save(path string, c Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".kopi-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// CreateTemp makes the file 0600 already; chmod keeps that guaranteed on
	// every platform (Windows ignores the mode bits here).
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

// Validate reports a config the app cannot run with.
func (c Config) Validate() error {
	switch {
	case c.DeviceName == "":
		return errors.New("config: device name is empty")
	case c.DeviceID == "":
		return errors.New("config: device id is empty")
	case c.Port < 1 || c.Port > 65535:
		return fmt.Errorf("config: port %d outside 1..65535", c.Port)
	}
	return nil
}

// String returns a human-readable summary; the passphrase appears only as
// "[set]" or "[not set]".
func (c Config) String() string {
	return fmt.Sprintf("Config{DeviceID: %q, DeviceName: %q, Passphrase: %s, IncludeSensitive: %t, Port: %d}",
		c.DeviceID, c.DeviceName, passphraseStatus(c.Passphrase), c.IncludeSensitive, c.Port)
}

// GoString redacts the passphrase the same way, so %#v does not leak it.
// Note: %#v of a non-pointer Config prints the GoStringer's output, and
// pointers get the "&" prefix from fmt, so both c and &c are safe.
func (c Config) GoString() string {
	return fmt.Sprintf("config.Config{DeviceID: %q, DeviceName: %q, Passphrase: %q, IncludeSensitive: %t, Port: %d}",
		c.DeviceID, c.DeviceName, passphraseStatus(c.Passphrase), c.IncludeSensitive, c.Port)
}

func passphraseStatus(p string) string {
	if p == "" {
		return "[not set]"
	}
	return "[set]"
}
