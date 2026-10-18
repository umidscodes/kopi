//go:build !windows && !darwin

package main

import "errors"

func dialogPassphrase(string) (string, error) {
	return "", errors.New("no dialog support on this platform; run `kopi passphrase` in a terminal")
}

func showMessage(string) {}
