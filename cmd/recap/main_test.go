package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

type exited int

// parse runs args through the real parser without exiting the test binary.
func parse(t *testing.T, args ...string) (*cli, *kong.Context, string, error) {
	t.Helper()
	var c cli
	var out bytes.Buffer
	parser, err := newParser(context.Background(), &c,
		kong.Writers(&out, &out),
		kong.Exit(func(code int) { panic(exited(code)) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	var k *kong.Context
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(exited); !ok {
					panic(r)
				}
			}
		}()
		k, err = parser.Parse(args)
	}()
	return &c, k, out.String(), err
}

func TestDraftFlags(t *testing.T) {
	c, k, _, err := parse(t, "draft", "--week", "2026-W39", "--no-ai", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if k.Command() != "draft" {
		t.Errorf("command = %q, want draft", k.Command())
	}
	want := draftCmd{Config: "recap.yaml", Issues: "issues", Week: "2026-W39", NoAI: true, Force: true, Footer: "footer.md"}
	if c.Draft != want {
		t.Errorf("draft = %+v, want %+v", c.Draft, want)
	}
}

func TestRenderFlags(t *testing.T) {
	c, k, _, err := parse(t, "render", "--no-clipboard", "--out", "x")
	if err != nil {
		t.Fatal(err)
	}
	if k.Command() != "render" {
		t.Errorf("command = %q, want render", k.Command())
	}
	want := renderCmd{Issues: "issues", Out: "x", NoClipboard: true}
	if c.Render != want {
		t.Errorf("render = %+v, want %+v", c.Render, want)
	}
}

func TestVersion(t *testing.T) {
	_, _, out, _ := parse(t, "--version")
	if strings.TrimSpace(out) != "recap dev" {
		t.Errorf("--version printed %q", out)
	}
	_, k, _, err := parse(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	k.Stdout = &buf
	if err := k.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "recap dev" {
		t.Errorf("version printed %q", buf.String())
	}
}

func TestParseErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"publish"},
		{"draft", "-week", "2026-W39"}, // single-dash long flags are not accepted
	} {
		if _, _, _, err := parse(t, args...); err == nil {
			t.Errorf("parse %q: want an error", args)
		}
	}
}
