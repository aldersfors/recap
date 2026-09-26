package config

import (
	"strings"
	"testing"
)

const valid = `
team: {name: Platform, audience: the product teams}
github:
  org: acme
  repos: [platform, aws]
  exclude_authors: ["renovate[bot]"]
  include_teams: [platform]
  include_authors: [carol]
  tickets_repo: project
ai:
  provider: bedrock
  anthropic: { model: claude-opus-5, inference_geo: global }
  bedrock: { region: eu-north-1, model: anthropic.claude-opus-5 }
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse(strings.NewReader(valid))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.GitHub.Org != "acme" || len(cfg.GitHub.Repos) != 2 {
		t.Errorf("github = %+v", cfg.GitHub)
	}
	if len(cfg.GitHub.IncludeTeams) != 1 || cfg.GitHub.IncludeAuthors[0] != "carol" {
		t.Errorf("include filter = %+v", cfg.GitHub)
	}
	if cfg.WindowWeeks != 1 {
		t.Errorf("WindowWeeks default = %d, want 1", cfg.WindowWeeks)
	}
	if cfg.GitHub.TicketsRepo != "project" {
		t.Errorf("tickets_repo = %q", cfg.GitHub.TicketsRepo)
	}
	if cfg.Team.Name != "Platform" || cfg.Team.Audience != "the product teams" {
		t.Errorf("team = %+v", cfg.Team)
	}
	if cfg.Forge != ForgeGitHub {
		t.Errorf("Forge default = %q, want github", cfg.Forge)
	}
	if cfg.AI.Anthropic.InferenceGeo != "global" {
		t.Errorf("anthropic inference_geo = %q", cfg.AI.Anthropic.InferenceGeo)
	}
	if cfg.AI.Bedrock.Model != "anthropic.claude-opus-5" {
		t.Errorf("bedrock model = %q", cfg.AI.Bedrock.Model)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name, yaml, wantErr string
	}{
		{"missing org", "github: {repos: [a]}\nai: {provider: anthropic, anthropic: {model: m}}", "github.org"},
		{"no repos or topics", "github: {org: o}\nai: {provider: anthropic, anthropic: {model: m}}", "github.repos"},
		{"unknown provider", "github: {org: o, repos: [a]}\nai: {provider: openai}", "ai.provider"},
		{"anthropic without model", "github: {org: o, repos: [a]}\nai: {provider: anthropic}", "ai.anthropic.model"},
		{"bedrock without region", "github: {org: o, repos: [a]}\nai: {provider: bedrock, bedrock: {model: m}}", "ai.bedrock.region"},
		{"negative window", "github: {org: o, repos: [a]}\nwindow_weeks: -1\nai: {provider: anthropic, anthropic: {model: m}}", "window_weeks"},
		{"unknown forge", "forge: bitbucket\ngithub: {org: o, repos: [a]}\nai: {provider: anthropic, anthropic: {model: m}}", "forge"},
		{"gitlab without group", "forge: gitlab\ngitlab: {projects: [a]}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.group"},
		{"gitlab without projects or topics", "forge: gitlab\ngitlab: {group: g}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.projects"},
		{"gitlab http base_url", "forge: gitlab\ngitlab: {group: g, projects: [a], base_url: \"http://git.corp\"}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.base_url"},
		{"gitlab relative base_url", "forge: gitlab\ngitlab: {group: g, projects: [a], base_url: git.corp}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.base_url"},
		{"tickets_repo without a team", "github: {org: o, repos: [a], tickets_repo: project}\nai: {provider: anthropic, anthropic: {model: m}}", "github.tickets_repo"},
		{"unknown field", "github: {org: o, repos: [a], repoz: [b]}\nai: {provider: anthropic, anthropic: {model: m}}", "repoz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTopicsOnlyIsValid(t *testing.T) {
	_, err := Parse(strings.NewReader("github: {org: o, topics: [ops]}\nai: {provider: anthropic, anthropic: {model: m}}"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestGitLabConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(`
forge: gitlab
gitlab:
  group: acme/ops
  projects: [infra]
  include_groups: [sre]
  include_authors: [bob@corp.example]
  exclude_bots: true
ai: {provider: anthropic, anthropic: {model: m}}
`))
	if err != nil {
		t.Fatalf("Parse: %v (a gitlab config needs no github block)", err)
	}
	gl := cfg.GitLab
	if gl.BaseURL != "https://gitlab.com" {
		t.Errorf("BaseURL default = %q", gl.BaseURL)
	}
	if gl.Group != "acme/ops" || gl.Projects[0] != "infra" || gl.IncludeGroups[0] != "sre" || gl.IncludeAuthors[0] != "bob@corp.example" || !gl.ExcludeBots {
		t.Errorf("gitlab = %+v", gl)
	}
}

func TestTeamDefaults(t *testing.T) {
	cfg, err := Parse(strings.NewReader("github: {org: o, repos: [a]}\nai: {provider: anthropic, anthropic: {model: m}}"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Team.Name != "Team" || cfg.Team.Audience != "the whole organization" {
		t.Errorf("team defaults = %+v", cfg.Team)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load("../../recap.example.yaml")
	if err != nil {
		t.Fatalf("recap.example.yaml: %v", err)
	}
	if cfg.Team.Name == "" || cfg.GitHub.Org == "" {
		t.Errorf("example config = %+v", cfg)
	}
}
