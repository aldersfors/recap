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

func TestParsePlane(t *testing.T) {
	cfg, err := Parse(strings.NewReader(`
forge: gitlab
gitlab: {group: g, projects: [a]}
plane:
  base_url: https://plane.example.com
  workspace: acme
  projects: [ENG, OPS]
  include_members: [alice@example.com]
ai: {provider: anthropic, anthropic: {model: m}}
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Plane.Enabled() || len(cfg.Plane.Projects) != 2 || cfg.Plane.IncludeMembers[0] != "alice@example.com" {
		t.Errorf("plane = %+v", cfg.Plane)
	}
	if strings.Contains(valid, "plane") {
		t.Fatal("valid must stay without plane")
	}
	cfg, err = Parse(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plane.Enabled() {
		t.Error("plane enabled without a workspace")
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
		{"converse without region", "github: {org: o, repos: [a]}\nai: {provider: converse, converse: {model: m}}", "ai.converse.region"},
		{"converse without model", "github: {org: o, repos: [a]}\nai: {provider: converse, converse: {region: eu-north-1}}", "ai.converse.model"},
		{"negative window", "github: {org: o, repos: [a]}\nwindow_weeks: -1\nai: {provider: anthropic, anthropic: {model: m}}", "window_weeks"},
		{"unknown forge", "forge: bitbucket\ngithub: {org: o, repos: [a]}\nai: {provider: anthropic, anthropic: {model: m}}", "forge"},
		{"gitlab without group", "forge: gitlab\ngitlab: {projects: [a]}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.group"},
		{"gitlab without projects or topics", "forge: gitlab\ngitlab: {group: g}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.projects"},
		{"gitlab http base_url", "forge: gitlab\ngitlab: {group: g, projects: [a], base_url: \"http://git.corp\"}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.base_url"},
		{"gitlab relative base_url", "forge: gitlab\ngitlab: {group: g, projects: [a], base_url: git.corp}\nai: {provider: anthropic, anthropic: {model: m}}", "gitlab.base_url"},
		{"tickets_repo without a team", "github: {org: o, repos: [a], tickets_repo: project}\nai: {provider: anthropic, anthropic: {model: m}}", "github.tickets_repo"},
		{"plane without base_url", "github: {org: o, repos: [a]}\nplane: {workspace: acme, projects: [ENG], include_members: [alice@example.com]}\nai: {provider: anthropic, anthropic: {model: m}}", "plane.base_url"},
		{"plane without projects", "github: {org: o, repos: [a]}\nplane: {base_url: \"https://plane.example.com\", workspace: acme, include_members: [alice@example.com]}\nai: {provider: anthropic, anthropic: {model: m}}", "plane.projects"},
		{"plane without members", "github: {org: o, repos: [a]}\nplane: {base_url: \"https://plane.example.com\", workspace: acme, projects: [ENG]}\nai: {provider: anthropic, anthropic: {model: m}}", "plane.include_members"},
		{"plane and tickets_repo", "github: {org: o, repos: [a], include_authors: [x], tickets_repo: project}\nplane: {base_url: \"https://plane.example.com\", workspace: acme, projects: [ENG], include_members: [alice@example.com]}\nai: {provider: anthropic, anthropic: {model: m}}", "not both"},
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

func TestClaudeCodeConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader("github: {org: o, repos: [a]}\nai: {provider: claude-code}"))
	if err != nil {
		t.Fatalf("Parse without model: %v", err)
	}
	if cfg.AI.ClaudeCode.Model != "" {
		t.Errorf("model = %q", cfg.AI.ClaudeCode.Model)
	}
	cfg, err = Parse(strings.NewReader("github: {org: o, repos: [a]}\nai: {provider: claude-code, claude_code: {model: m}}"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.AI.ClaudeCode.Model != "m" {
		t.Errorf("model = %q", cfg.AI.ClaudeCode.Model)
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
