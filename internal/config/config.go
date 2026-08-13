// Package config defines the on-disk configuration for dredge and
// handles loading, saving and defaulting.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Mode controls how a given resource type is handled during a run.
type Mode string

const (
	// ModeOff never touches this resource type.
	ModeOff Mode = "off"
	// ModeAuto prunes matching resources without asking.
	ModeAuto Mode = "auto"
	// ModeAsk sends an interactive approval request before pruning.
	ModeAsk Mode = "ask"
)

// ResourceRule configures a single resource type (images, volumes, ...).
type ResourceRule struct {
	Enabled bool `yaml:"enabled"`
	Mode    Mode `yaml:"mode"`
}

// Resources bundles the per-type rules.
type Resources struct {
	ImagesDangling ResourceRule `yaml:"images_dangling"`
	ImagesUnused   ResourceRule `yaml:"images_unused"`
	Containers     ResourceRule `yaml:"containers"`
	Networks       ResourceRule `yaml:"networks"`
	BuildCache     ResourceRule `yaml:"build_cache"`
	Volumes        ResourceRule `yaml:"volumes"`
}

// Safety holds the guard rails that protect resources from deletion.
type Safety struct {
	// MinAgeHours: only remove resources older than this. 0 disables the check.
	MinAgeHours int `yaml:"min_age_hours"`
	// ProtectLabels: resources carrying any of these labels (key=value or key)
	// are never removed.
	ProtectLabels []string `yaml:"protect_labels"`
	// ProtectVolumeNames: glob patterns; matching volume names are kept.
	ProtectVolumeNames []string `yaml:"protect_volume_names"`
	// ProtectImageRepos: glob patterns matched against repo:tag; kept.
	ProtectImageRepos []string `yaml:"protect_image_repos"`
	// DryRun: never delete anything, only report what would happen.
	DryRun bool `yaml:"dry_run"`
}

// Trigger gates whether a run actually does anything.
type Trigger struct {
	// MinReclaimableMB: skip the run unless at least this much is reclaimable.
	MinReclaimableMB int64 `yaml:"min_reclaimable_mb"`
	// MinDiskUsagePercent: only act when the Docker data-root disk is at least
	// this full. 0 disables the check.
	MinDiskUsagePercent int `yaml:"min_disk_usage_percent"`
}

// Telegram configures the interactive approval channel.
type Telegram struct {
	Enabled                bool   `yaml:"enabled"`
	BotToken               string `yaml:"bot_token"`
	ChatID                 string `yaml:"chat_id"`
	ApprovalTimeoutMinutes int    `yaml:"approval_timeout_minutes"`
	// OnTimeout: default action when no one responds in time.
	// One of: none | dangling | all
	OnTimeout string `yaml:"on_timeout"`
}

// Notify bundles all outbound channels.
type Notify struct {
	// URLs: shoutrrr service URLs used for the report (non-interactive) and the
	// final confirmation. e.g. smtp://, telegram://, ntfy://, gotify://, discord://
	URLs []string `yaml:"urls"`
	// Telegram is the interactive channel (buttons). Optional but recommended.
	Telegram Telegram `yaml:"telegram"`
	// ReportOnNothing: send a message even when there is nothing to clean.
	ReportOnNothing bool `yaml:"report_on_nothing"`
}

// Docker points at the Docker Engine API (typically via a socket-proxy).
type Docker struct {
	// Host: base URL of the engine API, e.g. http://dockerproxy:2375
	// or unix:///var/run/docker.sock
	Host string `yaml:"host"`
	// APIVersion: optional, prepended as /vX.YZ when set.
	APIVersion string `yaml:"api_version"`
	// TimeoutSeconds for individual API calls.
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// Schedule configures unattended runs.
type Schedule struct {
	// Cron expression (5 fields). Used by systemd-timer/internal scheduler.
	Cron string `yaml:"cron"`
	// Timezone, e.g. Europe/Berlin. "Local" uses the system timezone.
	Timezone string `yaml:"timezone"`
}

// Audit configures the run history log.
type Audit struct {
	LogFile string `yaml:"log_file"`
}

// Config is the root configuration object.
type Config struct {
	Docker    Docker    `yaml:"docker"`
	Schedule  Schedule  `yaml:"schedule"`
	Resources Resources `yaml:"resources"`
	Safety    Safety    `yaml:"safety"`
	Trigger   Trigger   `yaml:"trigger"`
	Notify    Notify    `yaml:"notify"`
	Audit     Audit     `yaml:"audit"`
}

// Default returns a sensible, safety-first default configuration.
func Default() *Config {
	return &Config{
		Docker: Docker{
			Host:           "unix:///var/run/docker.sock",
			TimeoutSeconds: 30,
		},
		Schedule: Schedule{
			Cron:     "0 9 * * 1", // Monday 09:00
			Timezone: "Local",
		},
		Resources: Resources{
			ImagesDangling: ResourceRule{Enabled: true, Mode: ModeAsk},
			ImagesUnused:   ResourceRule{Enabled: false, Mode: ModeAsk},
			Containers:     ResourceRule{Enabled: true, Mode: ModeAsk},
			Networks:       ResourceRule{Enabled: true, Mode: ModeAuto},
			BuildCache:     ResourceRule{Enabled: true, Mode: ModeAuto},
			Volumes:        ResourceRule{Enabled: true, Mode: ModeAsk}, // dangerous
		},
		Safety: Safety{
			MinAgeHours:        24,
			ProtectLabels:      []string{"janitor.keep=true"},
			ProtectVolumeNames: []string{},
			ProtectImageRepos:  []string{},
			DryRun:             false,
		},
		Trigger: Trigger{
			MinReclaimableMB:    0,
			MinDiskUsagePercent: 0,
		},
		Notify: Notify{
			URLs: []string{},
			Telegram: Telegram{
				Enabled:                true,
				ApprovalTimeoutMinutes: 120,
				OnTimeout:              "none",
			},
			ReportOnNothing: false,
		},
		Audit: Audit{
			LogFile: "/var/lib/dredge/history.jsonl",
		},
	}
}

// DefaultPath returns the standard config path, honouring DREDGE_CONFIG.
func DefaultPath() string {
	if p := os.Getenv("DREDGE_CONFIG"); p != "" {
		return p
	}
	// Prefer /etc when running as a service; fall back to XDG for a user setup.
	if _, err := os.Stat("/etc/dredge"); err == nil {
		return "/etc/dredge/config.yaml"
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dredge", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err == nil {
		return filepath.Join(home, ".config", "dredge", "config.yaml")
	}
	return "config.yaml"
}

// Load reads a config file. If the file does not exist it returns an error;
// callers that want a default should check with os.IsNotExist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default() // start from defaults so missing keys are sane
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config as YAML, creating parent directories as needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# dredge configuration\n" +
		"# See README for all options. Edit by hand or via `dredge tui`.\n"
	return os.WriteFile(path, append([]byte(header), data...), 0o600)
}

// Validate checks for obviously broken configuration and returns a list of
// human-readable problems (empty slice means OK).
func (c *Config) Validate() []string {
	var problems []string
	if c.Docker.Host == "" {
		problems = append(problems, "docker.host ist leer")
	}
	switch c.Notify.Telegram.OnTimeout {
	case "", "none", "dangling", "all":
	default:
		problems = append(problems, "notify.telegram.on_timeout muss none|dangling|all sein")
	}
	if c.needsInteractive() && !c.Notify.Telegram.Enabled {
		problems = append(problems, "mindestens ein Ressourcentyp steht auf 'ask', aber Telegram ist deaktiviert")
	}
	if c.Notify.Telegram.Enabled && (c.Notify.Telegram.BotToken == "" || c.Notify.Telegram.ChatID == "") {
		problems = append(problems, "Telegram aktiviert, aber bot_token oder chat_id fehlt")
	}
	return problems
}

// needsInteractive reports whether any enabled resource uses ask mode.
func (c *Config) needsInteractive() bool {
	for _, r := range c.Resources.All() {
		if r.Rule.Enabled && r.Rule.Mode == ModeAsk {
			return true
		}
	}
	return false
}

// NamedRule pairs a stable key/label with a rule for iteration.
type NamedRule struct {
	Key   string
	Label string
	Rule  ResourceRule
}

// All returns the rules in a stable, display-friendly order.
func (r Resources) All() []NamedRule {
	return []NamedRule{
		{"images_dangling", "Dangling Images", r.ImagesDangling},
		{"images_unused", "Ungenutzte Images", r.ImagesUnused},
		{"containers", "Gestoppte Container", r.Containers},
		{"networks", "Ungenutzte Netzwerke", r.Networks},
		{"build_cache", "Build-Cache", r.BuildCache},
		{"volumes", "Verwaiste Volumes", r.Volumes},
	}
}
