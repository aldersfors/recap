package summarize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// claudeCodeRunner runs the claude CLI with args, env and stdin and returns
// its stdout, so tests can stand in for the binary.
type claudeCodeRunner func(ctx context.Context, args, env []string, stdin string) ([]byte, error)

// claudeCodeProvider drafts through the claude CLI in print mode, so it
// authenticates the way Claude Code does: CLAUDE_CODE_OAUTH_TOKEN from
// `claude setup-token`, else the existing claude login.
type claudeCodeProvider struct {
	run   claudeCodeRunner
	model string
	// log receives where inference ran, when the CLI reports it. Nil discards.
	log io.Writer
}

type claudeCodeResult struct {
	Subtype    string `json:"subtype"`
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InferenceGeo string `json:"inference_geo"`
	} `json:"usage"`
}

func (p *claudeCodeProvider) name() string {
	if p.model == "" {
		return "claude"
	}
	return p.model
}

func (p *claudeCodeProvider) Complete(ctx context.Context, system, user string) (string, error) {
	// No tools, settings or MCP servers: the CLI only generates text.
	args := []string{
		"-p", "--output-format", "json",
		"--system-prompt", system,
		"--tools", "",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--no-session-persistence",
	}
	if p.model != "" {
		args = append(args, "--model", p.model)
	}
	out, runErr := p.run(ctx, args, claudeCodeEnv(os.Environ()), user)
	var res claudeCodeResult
	if err := json.Unmarshal(out, &res); err != nil {
		if runErr != nil {
			return "", fmt.Errorf("%s: %w", p.name(), runErr)
		}
		return "", fmt.Errorf("%s: parse output: %w", p.name(), err)
	}
	if p.log != nil && res.Usage.InferenceGeo != "" {
		_, _ = fmt.Fprintf(p.log, "Inference ran in %s.\n", res.Usage.InferenceGeo)
	}
	switch {
	case res.StopReason == "refusal":
		return "", fmt.Errorf("%s declined the request", p.name())
	case res.StopReason == "max_tokens":
		return "", fmt.Errorf("%s hit max_tokens before finishing the draft", p.name())
	case res.IsError || runErr != nil:
		msg := strings.TrimSpace(res.Result)
		if msg == "" {
			msg = res.Subtype
		}
		return "", fmt.Errorf("%s: %s", p.name(), msg)
	}
	return res.Result, nil
}

// claudeCodeEnv drops ANTHROPIC_API_KEY so the CLI uses the Claude Code
// login or CLAUDE_CODE_OAUTH_TOKEN, not an API key.
func claudeCodeEnv(environ []string) []string {
	env := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			env = append(env, kv)
		}
	}
	return env
}

// execClaude runs claude in an empty temp dir, so no CLAUDE.md is picked up.
func execClaude(ctx context.Context, args, env []string, stdin string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "recap-claude-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, errors.New("claude CLI not found on PATH: install Claude Code")
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}
