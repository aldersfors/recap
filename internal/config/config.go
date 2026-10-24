// Package config loads recap.yaml: which GitHub or GitLab repos to scan, which
// Plane projects hold the tickets, and which model backend drafts the summary.
// Credentials never live in this file.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"go.yaml.in/yaml/v3"
)

const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
)

const (
	ProviderAnthropic  = "anthropic"
	ProviderBedrock    = "bedrock"
	ProviderConverse   = "converse"
	ProviderClaudeCode = "claude-code"
)

type Config struct {
	Team        Team   `yaml:"team"`
	Forge       string `yaml:"forge"`
	GitHub      GitHub `yaml:"github"`
	GitLab      GitLab `yaml:"gitlab"`
	Plane       Plane  `yaml:"plane"`
	WindowWeeks int    `yaml:"window_weeks"`
	AI          AI     `yaml:"ai"`
}

// Team names who writes the update and who reads it; both go into the prompt.
type Team struct {
	Name     string `yaml:"name"`
	Audience string `yaml:"audience"`
}

type GitHub struct {
	Org            string   `yaml:"org"`
	Repos          []string `yaml:"repos"`
	Topics         []string `yaml:"topics"`
	ExcludeAuthors []string `yaml:"exclude_authors"`
	ExcludeBots    bool     `yaml:"exclude_bots"`
	IncludeTeams   []string `yaml:"include_teams"`
	IncludeAuthors []string `yaml:"include_authors"`
	// TicketsRepo, when set, is the org repo whose closed issues are counted
	// into the stats line under the summary.
	TicketsRepo string `yaml:"tickets_repo"`
}

type GitLab struct {
	BaseURL        string   `yaml:"base_url"`
	Group          string   `yaml:"group"`
	Projects       []string `yaml:"projects"`
	Topics         []string `yaml:"topics"`
	IncludeGroups  []string `yaml:"include_groups"`
	IncludeAuthors []string `yaml:"include_authors"`
	ExcludeAuthors []string `yaml:"exclude_authors"`
	ExcludeBots    bool     `yaml:"exclude_bots"`
}

// Plane, when Workspace is set, is the ticket tracker: work items completed in
// the window join the activity and the stats line, and references such as
// ENG-12 group the forge activity by epic. It works with either forge.
type Plane struct {
	BaseURL   string   `yaml:"base_url"`
	Workspace string   `yaml:"workspace"`
	Projects  []string `yaml:"projects"`
	// IncludeMembers are the emails whose completed work items count.
	IncludeMembers []string `yaml:"include_members"`
}

// Enabled reports whether a Plane workspace is configured.
func (p Plane) Enabled() bool { return p.Workspace != "" }

type AI struct {
	Provider   string     `yaml:"provider"`
	Anthropic  Anthropic  `yaml:"anthropic"`
	Bedrock    Bedrock    `yaml:"bedrock"`
	Converse   Bedrock    `yaml:"converse"`
	ClaudeCode ClaudeCode `yaml:"claude_code"`
}

type Anthropic struct {
	Model string `yaml:"model"`
	// InferenceGeo is sent as inference_geo. Empty uses the API default (global).
	InferenceGeo string `yaml:"inference_geo"`
}

// ClaudeCode drafts through the claude CLI. Model is optional; empty uses the
// CLI default.
type ClaudeCode struct {
	Model string `yaml:"model"`
}

// Bedrock configures both Bedrock providers: bedrock (Anthropic models on the
// Mantle endpoint) and converse (any model through the Converse API).
type Bedrock struct {
	Region  string `yaml:"region"`
	Model   string `yaml:"model"`
	Profile string `yaml:"profile"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	cfg, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.WindowWeeks == 0 {
		cfg.WindowWeeks = 1
	}
	if cfg.Team.Name == "" {
		cfg.Team.Name = "Team"
	}
	if cfg.Team.Audience == "" {
		cfg.Team.Audience = "the whole organization"
	}
	if cfg.Forge == "" {
		cfg.Forge = ForgeGitHub
	}
	if cfg.GitLab.BaseURL == "" {
		cfg.GitLab.BaseURL = "https://gitlab.com"
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	switch c.Forge {
	case ForgeGitHub:
		if c.GitHub.Org == "" {
			errs = append(errs, errors.New("github.org is required"))
		}
		if len(c.GitHub.Repos) == 0 && len(c.GitHub.Topics) == 0 {
			errs = append(errs, errors.New("github.repos or github.topics must list at least one entry"))
		}
		if c.GitHub.TicketsRepo != "" && len(c.GitHub.IncludeTeams) == 0 && len(c.GitHub.IncludeAuthors) == 0 {
			errs = append(errs, errors.New("github.tickets_repo counts only issues assigned to the team, so it needs github.include_teams or github.include_authors"))
		}
	case ForgeGitLab:
		if c.GitLab.Group == "" {
			errs = append(errs, errors.New("gitlab.group is required"))
		}
		if len(c.GitLab.Projects) == 0 && len(c.GitLab.Topics) == 0 {
			errs = append(errs, errors.New("gitlab.projects or gitlab.topics must list at least one entry"))
		}
		if u, err := url.Parse(c.GitLab.BaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, fmt.Errorf("gitlab.base_url must be an absolute https URL, got %q", c.GitLab.BaseURL))
		}
	default:
		errs = append(errs, fmt.Errorf("forge must be %q or %q, got %q", ForgeGitHub, ForgeGitLab, c.Forge))
	}
	if c.Plane.Enabled() {
		if u, err := url.Parse(c.Plane.BaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, fmt.Errorf("plane.base_url must be an absolute https URL, got %q", c.Plane.BaseURL))
		}
		if len(c.Plane.Projects) == 0 {
			errs = append(errs, errors.New("plane.projects must list at least one project identifier"))
		}
		if len(c.Plane.IncludeMembers) == 0 {
			errs = append(errs, errors.New("plane counts only work items assigned to the team, so it needs plane.include_members"))
		}
		if c.Forge == ForgeGitHub && c.GitHub.TicketsRepo != "" {
			errs = append(errs, errors.New("set either github.tickets_repo or plane, not both: there is one ticket source"))
		}
	}
	if c.WindowWeeks < 1 {
		errs = append(errs, errors.New("window_weeks must be 1 or more"))
	}
	switch c.AI.Provider {
	case ProviderAnthropic:
		if c.AI.Anthropic.Model == "" {
			errs = append(errs, errors.New("ai.anthropic.model is required"))
		}
	case ProviderBedrock:
		if c.AI.Bedrock.Region == "" {
			errs = append(errs, errors.New("ai.bedrock.region is required"))
		}
		if c.AI.Bedrock.Model == "" {
			errs = append(errs, errors.New("ai.bedrock.model is required"))
		}
	case ProviderConverse:
		if c.AI.Converse.Region == "" {
			errs = append(errs, errors.New("ai.converse.region is required"))
		}
		if c.AI.Converse.Model == "" {
			errs = append(errs, errors.New("ai.converse.model is required"))
		}
	case ProviderClaudeCode:
	default:
		errs = append(errs, fmt.Errorf("ai.provider must be %q, %q, %q or %q, got %q", ProviderAnthropic, ProviderBedrock, ProviderConverse, ProviderClaudeCode, c.AI.Provider))
	}
	return errors.Join(errs...)
}
