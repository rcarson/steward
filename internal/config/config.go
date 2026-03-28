package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// rawDefaults mirrors the YAML defaults block before merging.
type rawDefaults struct {
	PollInterval int    `yaml:"poll_interval"`
	Branch       string `yaml:"branch"`
	WorkDir      string `yaml:"work_dir"`
	Token        string `yaml:"token"`
}

// rawStack mirrors a single stack entry as read from YAML.
type rawStack struct {
	Name         string `yaml:"name"`
	Repo         string `yaml:"repo"`
	Path         string `yaml:"path"`
	Branch       string `yaml:"branch"`
	Token        string `yaml:"token"`
	EnvFile      string `yaml:"env_file"`
	PollInterval int    `yaml:"poll_interval"`
}

// rawConfig is the top-level YAML structure.
type rawConfig struct {
	Defaults rawDefaults `yaml:"defaults"`
	Stacks   []rawStack  `yaml:"stacks"`
}

// StackConfig is the fully-resolved configuration for a single stack.
type StackConfig struct {
	Name         string
	Repo         string
	Path         string
	Branch       string
	Token        string
	EnvFile      string
	WorkDir      string
	PollInterval int
}

// Config is the fully-resolved top-level configuration.
type Config struct {
	Defaults rawDefaults
	Stacks   []StackConfig
}

var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// interpolate replaces ${VAR} references with their values from the environment.
// Unset variables resolve to an empty string.
func interpolate(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := envVarRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return ""
		}
		return os.Getenv(sub[1])
	})
}

// interpolateDefaults resolves env vars in the defaults block.
func interpolateDefaults(d rawDefaults) rawDefaults {
	d.Branch = interpolate(d.Branch)
	d.WorkDir = interpolate(d.WorkDir)
	d.Token = interpolate(d.Token)
	return d
}

// mergeStack applies defaults to a raw stack entry, then interpolates env vars.
// Per-stack non-zero/non-empty values take precedence over defaults.
func mergeStack(raw rawStack, defaults rawDefaults) StackConfig {
	branch := defaults.Branch
	if raw.Branch != "" {
		branch = raw.Branch
	}
	branch = interpolate(branch)

	token := defaults.Token
	if raw.Token != "" {
		token = raw.Token
	}
	token = interpolate(token)

	pollInterval := defaults.PollInterval
	if raw.PollInterval != 0 {
		pollInterval = raw.PollInterval
	}

	return StackConfig{
		Name:         interpolate(raw.Name),
		Repo:         interpolate(raw.Repo),
		Path:         interpolate(raw.Path),
		Branch:       branch,
		Token:        token,
		EnvFile:      interpolate(raw.EnvFile),
		WorkDir:      defaults.WorkDir,
		PollInterval: pollInterval,
	}
}

// isHTTPS returns true only when the URL scheme is https.
func isHTTPS(url string) bool {
	return strings.HasPrefix(strings.ToLower(url), "https://")
}

// redactToken removes the literal token value from an error string so it
// never leaks into user-visible messages.
func redactToken(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[REDACTED]")
}

// Load reads and validates the YAML config at path, merges defaults into each
// stack, and resolves ${ENV_VAR} references from the process environment.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading file: %w", err)
	}

	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: parsing YAML: %w", err)
	}

	// Resolve env vars in defaults.
	defaults := interpolateDefaults(raw.Defaults)

	// Apply default poll_interval when not set.
	const defaultPollInterval = 60
	if defaults.PollInterval == 0 {
		defaults.PollInterval = defaultPollInterval
	}

	// Validate and merge stacks.
	seenNames := make(map[string]struct{}, len(raw.Stacks))
	stacks := make([]StackConfig, 0, len(raw.Stacks))

	for i, rs := range raw.Stacks {
		// Validate required fields before interpolation so the raw values are
		// available for clear error reporting.
		if rs.Name == "" {
			return nil, fmt.Errorf("config: stack[%d]: name is required", i)
		}
		if rs.Repo == "" {
			return nil, fmt.Errorf("config: stack %q: repo is required", rs.Name)
		}
		if rs.Path == "" {
			return nil, fmt.Errorf("config: stack %q: path is required", rs.Name)
		}

		merged := mergeStack(rs, defaults)

		// Validate uniqueness of name (after interpolation).
		if _, exists := seenNames[merged.Name]; exists {
			return nil, fmt.Errorf("config: duplicate stack name %q", merged.Name)
		}
		seenNames[merged.Name] = struct{}{}

		// Validate repo URL scheme.
		if !isHTTPS(merged.Repo) {
			return nil, fmt.Errorf("config: stack %q: repo must use HTTPS URL (got %q); SSH and other protocols are not supported", merged.Name, merged.Repo)
		}

		// Validate poll_interval minimum.
		const minPollInterval = 10
		if merged.PollInterval < minPollInterval {
			// Build the error without the token.
			msg := fmt.Sprintf("config: stack %q: poll_interval must be at least %d seconds (got %d)", merged.Name, minPollInterval, merged.PollInterval)
			msg = redactToken(msg, merged.Token)
			return nil, fmt.Errorf("%s", msg)
		}

		stacks = append(stacks, merged)
	}

	return &Config{
		Defaults: defaults,
		Stacks:   stacks,
	}, nil
}
