package summarize

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type claudeCodeCall struct {
	args, env []string
	stdin     string
}

func fakeClaudeCode(out string, runErr error) (*claudeCodeProvider, *claudeCodeCall) {
	var got claudeCodeCall
	run := func(_ context.Context, args, env []string, stdin string) ([]byte, error) {
		got = claudeCodeCall{args: args, env: env, stdin: stdin}
		return []byte(out), runErr
	}
	return &claudeCodeProvider{run: run}, &got
}

func TestClaudeCodeRequestAndText(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-must-not-pass")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-token")
	p, got := fakeClaudeCode(`{"type":"result","subtype":"success","is_error":false,"result":"Draft body","stop_reason":"end_turn","usage":{"inference_geo":"global"}}`, nil)
	p.model = "claude-opus-5"
	var log strings.Builder
	p.log = &log
	text, err := p.Complete(context.Background(), "SYSTEM", "USER")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Draft body" {
		t.Errorf("text = %q", text)
	}
	if got.stdin != "USER" {
		t.Errorf("stdin = %q", got.stdin)
	}
	args := strings.Join(got.args, " ")
	for _, want := range []string{"-p", "--output-format json", "--system-prompt SYSTEM", "--tools ", "--model claude-opus-5"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if slices.ContainsFunc(got.env, func(kv string) bool { return strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") }) {
		t.Error("ANTHROPIC_API_KEY reached the CLI")
	}
	if !slices.Contains(got.env, "CLAUDE_CODE_OAUTH_TOKEN=oauth-token") {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN did not reach the CLI")
	}
	if !strings.Contains(log.String(), "Inference ran in global.") {
		t.Errorf("log = %q", log.String())
	}
}

func TestClaudeCodeNoModel(t *testing.T) {
	p, got := fakeClaudeCode(`{"result":"ok"}`, nil)
	if _, err := p.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got.args, "--model") {
		t.Errorf("args = %q", got.args)
	}
}

func TestClaudeCodeErrors(t *testing.T) {
	tests := []struct {
		name, out string
		runErr    error
		wantErr   string
	}{
		{"error result", `{"subtype":"success","is_error":true,"result":"Invalid API key"}`, errors.New("exit status 1"), "Invalid API key"},
		{"error subtype", `{"subtype":"error_during_execution","is_error":true}`, nil, "error_during_execution"},
		{"max tokens", `{"result":"partial","stop_reason":"max_tokens"}`, nil, "max_tokens"},
		{"refusal", `{"result":"","stop_reason":"refusal"}`, nil, "declined"},
		{"no json", ``, errors.New("claude CLI not found on PATH"), "not found"},
		{"bad json", `not json`, nil, "parse output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := fakeClaudeCode(tt.out, tt.runErr)
			_, err := p.Complete(context.Background(), "s", "u")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
