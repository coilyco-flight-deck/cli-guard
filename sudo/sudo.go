// Package sudo is policy-free plumbing for driving an interactive sudo
// over any transport that pipes a process's stdin without either (a)
// carrying a password
package sudo

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrNoTTY is returned by ReadPassword when /dev/tty cannot be opened.
// CI and headless invocations should fail fast on this rather than hang
var ErrNoTTY = errors.New("sudo: no controlling tty; cannot prompt for password")

// ReadPassword prompts on /dev/tty (NOT stdin, which the caller will
// reuse to pipe the password into the remote sudo) and returns the
func ReadPassword(prompt string) ([]byte, error) {
	fd, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, ErrNoTTY
	}
	defer func() { _ = fd.Close() }()

	if _, err := fmt.Fprint(fd, prompt); err != nil {
		return nil, fmt.Errorf("sudo: write prompt: %w", err)
	}
	pw, err := term.ReadPassword(int(fd.Fd())) //nolint:gosec // fd from os.OpenFile fits in int on every supported platform
	// Always emit a trailing newline so the next stderr line doesn't
	// collide with the prompt position.
	_, _ = fmt.Fprintln(fd)
	if err != nil {
		return nil, fmt.Errorf("sudo: read password: %w", err)
	}
	return pw, nil
}

// Zero overwrites b in place. Use with `defer` after a successful
// ReadPassword to keep the password-bearing buffer alive only as long
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// PasswordRequired returns true when stderr from a `sudo -n` attempt
// indicates the failure was "password needed" rather than a genuine
func PasswordRequired(stderr string) bool {
	for _, sentinel := range passwordSentinels {
		if strings.Contains(stderr, sentinel) {
			return true
		}
	}
	return false
}

var passwordSentinels = []string{
	"a password is required",
	"a terminal is required",
	"sorry, a password is required",
}
