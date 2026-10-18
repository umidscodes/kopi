package main

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/umidscodes/kopi/internal/config"
)

// MinPassphraseRunes is the shortest passphrase Kopi accepts.
const MinPassphraseRunes = 8

var (
	errCancelled = errors.New("cancelled")
	errMismatch  = errors.New("the two passphrases don't match")
	errTooShort  = fmt.Errorf("passphrase must be at least %d characters", MinPassphraseRunes)
)

// cmdPassphrase implements `kopi passphrase`. A running Kopi picks the change up
// from the config file within a few seconds.
func cmdPassphrase() int {
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	p, err := askPassphrase(interactive)
	if errors.Is(err, errCancelled) {
		return 1
	}
	if err == nil {
		err = savePassphrase(p)
	}
	if err != nil {
		tell(interactive, "Passphrase not saved: "+err.Error())
		return 1
	}
	tell(interactive, "Passphrase saved. Use the same passphrase on your other devices.")
	return 0
}

// askPassphrase asks twice, in the terminal or with OS dialogs.
func askPassphrase(interactive bool) (string, error) {
	ask := dialogPassphrase
	if interactive {
		ask = terminalPassphrase
	}
	p1, err := ask("Kopi passphrase (same on all your devices):")
	if err != nil {
		return "", err
	}
	if err := checkPassphrase(p1); err != nil {
		return "", err
	}
	p2, err := ask("Type the passphrase again:")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errMismatch
	}
	return p1, nil
}

func checkPassphrase(p string) error {
	if utf8.RuneCountInString(p) < MinPassphraseRunes {
		return errTooShort
	}
	return nil
}

func terminalPassphrase(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt+" ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func savePassphrase(p string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg.Passphrase = p
	return config.Save(path, cfg)
}

func tell(interactive bool, msg string) {
	if interactive {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	showMessage(msg)
}
