// Package config resolves settings from environment variables and flags.
package config

import (
	"errors"
	"strings"

	"github.com/davidhfrankelcodes/hubtui/internal/hub"
)

// Environment variables that enable authenticated requests.
const (
	EnvUsername = "DOCKERHUB_USERNAME"
	EnvToken    = "DOCKERHUB_TOKEN"
)

// Config is the resolved configuration.
type Config struct {
	Username string
	Token    hub.Secret
}

// Authenticated reports whether credentials are configured.
func (c Config) Authenticated() bool { return c.Token != "" }

// FromEnv reads the configuration. Anonymous access is the default; both
// credentials or neither must be set, since half a login is always a mistake
// worth reporting rather than silently ignoring.
func FromEnv(getenv func(string) string) (Config, error) {
	// Trim because a pasted token often carries a trailing newline.
	user := strings.TrimSpace(getenv(EnvUsername))
	token := strings.TrimSpace(getenv(EnvToken))
	switch {
	case user == "" && token == "":
		return Config{}, nil
	case user == "":
		return Config{}, errors.New(EnvToken + " is set but " + EnvUsername + " is not; set both or neither")
	case token == "":
		return Config{}, errors.New(EnvUsername + " is set but " + EnvToken + " is not; set both or neither")
	}
	return Config{Username: user, Token: hub.Secret(token)}, nil
}
