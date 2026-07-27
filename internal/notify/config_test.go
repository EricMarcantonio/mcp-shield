package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadConfigMissingFileDisablesNotifications pins the default posture.
// Notifications are opt-in: an operator who never wrote a config file gets
// the pre-notification behaviour, not a startup error.
func TestLoadConfigMissingFileDisablesNotifications(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing config file is not an error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config (notifications disabled), got %+v", cfg)
	}
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `{"webhooks":[{"name":"ops","url":"https://example.test/hook"}]}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Events) != 1 || cfg.Events[0] != "manifest.pending" {
		t.Fatalf("expected the pending event to be subscribed by default, got %v", cfg.Events)
	}
	if cfg.MaxAttempts != DefaultMaxAttempts {
		t.Fatalf("expected MaxAttempts to default to %d, got %d", DefaultMaxAttempts, cfg.MaxAttempts)
	}
	if cfg.DashboardURL != "" {
		t.Fatalf("expected no dashboard URL when none is configured, got %q", cfg.DashboardURL)
	}
}

// TestLoadConfigExpandsEnvironmentReferences is what lets an operator keep
// the webhook URL and HMAC secret out of the config file entirely — the
// file names an environment variable and the secret lives in the process
// environment or a secret manager.
func TestLoadConfigExpandsEnvironmentReferences(t *testing.T) {
	t.Setenv("TEST_HOOK_URL", "https://hooks.example.test/T000/B000/xyz")
	t.Setenv("TEST_HOOK_SECRET", "s3cret")
	path := writeConfig(t, `{"webhooks":[{"name":"ops","url":"$TEST_HOOK_URL","secret":"$TEST_HOOK_SECRET"}]}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := cfg.Webhooks[0]
	if got.URL != "https://hooks.example.test/T000/B000/xyz" {
		t.Fatalf("URL was not expanded: %q", got.URL)
	}
	if got.Secret != "s3cret" {
		t.Fatalf("secret was not expanded: %q", got.Secret)
	}
}

func TestLoadConfigRejectsUnusableTargets(t *testing.T) {
	for name, body := range map[string]string{
		"no name":        `{"webhooks":[{"url":"https://example.test/hook"}]}`,
		"no url":         `{"webhooks":[{"name":"ops"}]}`,
		"unknown format": `{"webhooks":[{"name":"ops","url":"https://example.test/h","format":"teams"}]}`,
		"malformed json": `{"webhooks":`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("expected an error: a target the dispatcher cannot use must fail at startup, not at 3am")
			}
		})
	}
}

// TestLoadConfigRejectsAnEmptyTargetList catches the config that looks
// configured but silently notifies nobody.
func TestLoadConfigRejectsAnEmptyTargetList(t *testing.T) {
	if _, err := LoadConfig(writeConfig(t, `{"webhooks":[]}`)); err == nil {
		t.Fatal("expected an error: a config file with no targets notifies nobody")
	}
}

// TestLoadConfigErrorNeverLeaksTheURL — a webhook URL is itself a
// capability-bearing credential. It must not reach an error string, which
// is the most likely thing an operator pastes into an issue tracker.
func TestLoadConfigErrorNeverLeaksTheURL(t *testing.T) {
	const secretURL = "https://hooks.slack.test/services/T0/B0/supersecrettoken"
	path := writeConfig(t, `{"webhooks":[{"name":"ops","url":"`+secretURL+`","format":"teams"}]}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected an error for the unknown format")
	}
	assertNoSecretIn(t, err.Error(), secretURL)
}
