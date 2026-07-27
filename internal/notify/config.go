package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Rendering formats a webhook target can request.
const (
	FormatJSON  = ""      // the Event struct verbatim
	FormatSlack = "slack" // Slack/Discord-compatible {"text": ...}
)

// DefaultMaxAttempts is how many failed deliveries a target gets before the
// dispatcher stops retrying and the row becomes visible as permanently
// failed. Six attempts spans roughly 15 hours on the default backoff.
const DefaultMaxAttempts = 6

// Config is the notification configuration, loaded from a file that holds
// webhook URLs and HMAC secrets — both capability-bearing credentials. Keep
// it out of version control and mode 0600.
type Config struct {
	Webhooks    []WebhookConfig `json:"webhooks"`
	Events      []string        `json:"events"`       // event types to deliver; default ["manifest.pending"]
	MaxAttempts int             `json:"max_attempts"` // default DefaultMaxAttempts

	// DashboardURL is the externally reachable base URL of the approval
	// dashboard, used to build the deep link in each event. Empty omits the
	// link rather than guessing a hostname the approver cannot reach.
	DashboardURL string `json:"dashboard_url"`
}

// WebhookConfig describes one delivery target. URL and Secret run through
// os.ExpandEnv, so the file can name environment variables instead of
// embedding the credentials.
type WebhookConfig struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
	Format string `json:"format"`
}

// LoadConfig reads the notification config. A missing file is not an error:
// notifications are opt-in, and an operator who never configured them gets
// (nil, nil) — disabled.
//
// Everything else is an error at startup. A target the dispatcher cannot
// use is worse than no target at all, because it looks configured; the
// whole point of this feature is that nobody silently hears nothing.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is operator-supplied configuration, not attacker input
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // absence is the documented "notifications disabled" signal, not a lookup miss
	}
	if err != nil {
		return nil, fmt.Errorf("notify: read config: %w", err)
	}
	warnIfConfigIsReadableByOthers(path)

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("notify: parse %s: %w", path, err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalize applies defaults and rejects a configuration that would notify
// nobody or fail at delivery time.
func (c *Config) normalize() error {
	if len(c.Webhooks) == 0 {
		return errors.New("notify: config lists no webhooks; remove the file to disable notifications instead of configuring zero targets")
	}
	for i := range c.Webhooks {
		if err := c.Webhooks[i].normalize(); err != nil {
			return err
		}
	}
	if len(c.Events) == 0 {
		c.Events = []string{"manifest.pending"}
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	return nil
}

// normalize validates one target. Error messages name the target, never its
// URL: an error string is the thing an operator pastes into a bug report.
func (w *WebhookConfig) normalize() error {
	w.URL = os.ExpandEnv(w.URL)
	w.Secret = os.ExpandEnv(w.Secret)

	if w.Name == "" {
		return errors.New("notify: every webhook needs a name; it is how the target is identified in logs, since its URL is a secret")
	}
	if w.URL == "" {
		return fmt.Errorf("notify: webhook %q has an empty url (an unset environment variable expands to nothing)", w.Name)
	}
	if w.Format != FormatJSON && w.Format != FormatSlack {
		return fmt.Errorf("notify: webhook %q has unknown format %q; use \"\" for raw JSON or %q", w.Name, w.Format, FormatSlack)
	}
	return nil
}

// warnIfConfigIsReadableByOthers reports a config file other local users can
// read. It holds webhook URLs and HMAC secrets. This warns rather than
// refusing to start, matching how the database directory is handled:
// silently tightening — or rejecting — a file the operator deliberately
// created (a Kubernetes secret mount, for instance) would be the greater
// surprise.
func warnIfConfigIsReadableByOthers(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn("notification config is readable by other users; it holds webhook URLs and HMAC secrets",
			"path", path, "mode", fmt.Sprintf("%#o", perm), "recommended", "0600")
	}
}
