// Command recap drafts and renders a team's weekly update from GitHub or GitLab
// activity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/jalet/recap/internal/activity"
	"github.com/jalet/recap/internal/config"
	"github.com/jalet/recap/internal/issue"
	"github.com/jalet/recap/internal/render"
	"github.com/jalet/recap/internal/summarize"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// cli is the command line: one field per subcommand.
type cli struct {
	Version    kong.VersionFlag `help:"Print the version and exit."`
	Draft      draftCmd         `cmd:"" help:"Collect GitHub or GitLab activity and write issues/<week>/index.md."`
	Render     renderCmd        `cmd:"" help:"Turn an issue into out/<week>/mattermost.md and email.html."`
	VersionCmd versionCmd       `cmd:"" name:"version" help:"Print the version."`
}

type draftCmd struct {
	Config string `default:"recap.yaml" help:"Config file."`
	Issues string `default:"issues" help:"Folder holding one folder per week."`
	Week   string `help:"ISO week to draft, e.g. 2026-W39 (default: the current week)."`
	Weeks  int    `help:"How many weeks of activity to include, ending with --week (default: window_weeks from config)."`
	NoAI   bool   `name:"no-ai" help:"Write a skeleton with the raw activity instead of asking Claude."`
	Force  bool   `help:"Replace an existing index.md."`
	Footer string `default:"footer.md" help:"Links section appended below the draft (skipped if missing)."`
}

type renderCmd struct {
	Issues      string `default:"issues" help:"Folder holding one folder per week."`
	Out         string `default:"out" help:"Folder for rendered output."`
	Week        string `help:"ISO week to render (default: the newest folder under --issues)."`
	NoClipboard bool   `name:"no-clipboard" help:"Do not copy the Mattermost text to the clipboard."`
}

type versionCmd struct{}

func (versionCmd) Run(k *kong.Context) error {
	_, err := fmt.Fprintln(k.Stdout, "recap", version)
	return err
}

// newParser builds the parser for c; ctx is handed to commands that need it.
func newParser(ctx context.Context, c *cli, opts ...kong.Option) (*kong.Kong, error) {
	return kong.New(c, append([]kong.Option{
		kong.Name("recap"),
		kong.Description("Draft and render a team's weekly update from GitHub or GitLab activity."),
		kong.Vars{"version": "recap " + version},
		kong.BindTo(ctx, (*context.Context)(nil)),
	}, opts...)...)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var c cli
	parser, err := newParser(ctx, &c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	k, err := parser.Parse(os.Args[1:])
	if err != nil {
		var perr *kong.ParseError
		if errors.As(err, &perr) {
			_ = perr.Context.PrintUsage(true)
			fmt.Fprintln(os.Stderr)
		}
		parser.Errorf("%s", err)
		os.Exit(2)
	}
	if err := k.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func (c *draftCmd) Run(ctx context.Context) error {
	cfg, err := config.Load(c.Config)
	if err != nil {
		return err
	}
	weeks := c.Weeks
	if weeks == 0 {
		weeks = cfg.WindowWeeks
	}
	now := time.Now().UTC()
	week := issue.WeekOf(now)
	if c.Week != "" {
		if week, err = issue.ParseWeek(c.Week); err != nil {
			return err
		}
	}
	from, to := week.Window(weeks)
	if to.After(now) {
		to = now
	}

	dir := issue.Dir(c.Issues, week)
	indexPath := filepath.Join(dir, "index.md")
	if _, err := os.Stat(indexPath); err == nil && !c.Force {
		return fmt.Errorf("%s: %w (use --force to replace it)", indexPath, issue.ErrExists)
	}

	collector, source, err := newCollector(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Collecting %s activity from %s to %s...\n", source, from.Format(time.DateOnly), to.Format(time.DateOnly))
	items, err := collector.Collect(ctx, from, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Found %d items.\n", len(items))

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(activity.ForStorage(items), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "activity.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}

	fm := issue.FrontMatter{
		Title:  cfg.Team.Name + " recap " + week.String(),
		Week:   week.String(),
		Period: fmt.Sprintf("%s to %s", from.Format(time.DateOnly), to.Add(-time.Nanosecond).Format(time.DateOnly)),
	}
	body := summarize.Skeleton(items)
	if !c.NoAI {
		provider, err := summarize.New(ctx, cfg.AI)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Drafting with %s...\n", cfg.AI.Provider)
		if body, err = summarize.Draft(ctx, provider, brief(cfg), fm, items); err != nil {
			return err
		}
	}
	if cfg.Forge == config.ForgeGitHub && cfg.GitHub.TicketsRepo != "" {
		stats, err := closedTickets(ctx, cfg, from, to)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Closed in %s: %d tickets, %d epics.\n", cfg.GitHub.TicketsRepo, stats.Tickets, stats.Epics)
		body = issue.WithStats(body, stats.Line(weeks))
	}
	footer, err := issue.ReadFooter(c.Footer)
	if err != nil {
		return err
	}
	body = issue.WithFooter(body, footer)
	if err := issue.WriteNew(indexPath, fm.Render()+body, c.Force); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\nEdit it, add media to %s, then run: recap render --week %s\n", indexPath, filepath.Join(dir, "media"), week)
	return nil
}

// brief carries the team and ticket format from cfg into the prompt.
func brief(cfg *config.Config) summarize.Brief {
	b := summarize.Brief{Team: cfg.Team.Name, Audience: cfg.Team.Audience}
	if cfg.Forge == config.ForgeGitHub && cfg.GitHub.TicketsRepo != "" {
		b.TicketRef = cfg.GitHub.Org + "/" + cfg.GitHub.TicketsRepo + "#123"
	}
	return b
}

// closedTickets counts the completed issues in github.tickets_repo for the
// window, filtered by the same include lists as the activity.
func closedTickets(ctx context.Context, cfg *config.Config, from, to time.Time) (activity.TicketStats, error) {
	token, err := activity.Token()
	if err != nil {
		return activity.TicketStats{}, err
	}
	gh := cfg.GitHub
	return activity.NewClient(token).ClosedTickets(ctx, activity.Query{
		Org: gh.Org, IncludeTeams: gh.IncludeTeams, IncludeAuthors: gh.IncludeAuthors,
		From: from, To: to,
	}, gh.TicketsRepo)
}

// newCollector returns the collector for cfg.Forge and a label for progress output.
func newCollector(cfg *config.Config) (activity.Collector, string, error) {
	if cfg.Forge == config.ForgeGitLab {
		gl := cfg.GitLab
		u, err := url.Parse(gl.BaseURL)
		if err != nil {
			return nil, "", err
		}
		token, err := activity.GitLabToken(u.Host)
		if err != nil {
			return nil, "", err
		}
		return activity.GitLabCollector{
			Client: activity.NewGitLab(gl.BaseURL, token),
			Query: activity.GitLabQuery{
				Group: gl.Group, Projects: gl.Projects, Topics: gl.Topics,
				IncludeGroups: gl.IncludeGroups, IncludeAuthors: gl.IncludeAuthors,
				ExcludeAuthors: gl.ExcludeAuthors, ExcludeBots: gl.ExcludeBots,
			},
		}, u.Host + "/" + gl.Group, nil
	}
	token, err := activity.Token()
	if err != nil {
		return nil, "", err
	}
	gh := cfg.GitHub
	return activity.GitHubCollector{
		Client: activity.NewClient(token),
		Query: activity.Query{
			Org: gh.Org, Repos: gh.Repos, Topics: gh.Topics,
			ExcludeAuthors: gh.ExcludeAuthors, ExcludeBots: gh.ExcludeBots,
			IncludeTeams: gh.IncludeTeams, IncludeAuthors: gh.IncludeAuthors,
		},
	}, gh.Org, nil
}

func (c *renderCmd) Run() error {
	var week issue.Week
	var err error
	if c.Week != "" {
		week, err = issue.ParseWeek(c.Week)
	} else {
		week, err = issue.Latest(c.Issues)
	}
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(issue.Dir(c.Issues, week))
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		return err
	}
	out, err := render.Render(dir, string(content))
	if err != nil {
		return err
	}

	target := filepath.Join(c.Out, week.String())
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	mmPath := filepath.Join(target, "mattermost.md")
	emailPath := filepath.Join(target, "email.html")
	emlPath := filepath.Join(target, "email.eml")
	if err := os.WriteFile(mmPath, []byte(out.Mattermost), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(emailPath, []byte(out.Email), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(emlPath, out.EML, 0o644); err != nil {
		return err
	}

	fmt.Printf("Mattermost: %s\nEmail:      %s (open in a browser, select all, paste into Outlook)\n", mmPath, emailPath)
	fmt.Printf("Draft:      %s (open in Outlook, address and send; videos are attached)\n", emlPath)
	if !c.NoClipboard {
		if err := copyToClipboard(out.Mattermost); err == nil {
			fmt.Println("Mattermost text copied to the clipboard.")
		}
	}
	if len(out.Media) > 0 {
		fmt.Println("\nAttach these files to the Mattermost post (and videos to a pasted email):")
		for _, m := range out.Media {
			fmt.Println("  " + m)
		}
	}
	for _, w := range out.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return nil
}

func copyToClipboard(text string) error {
	path, err := exec.LookPath("pbcopy")
	if err != nil {
		return errors.New("pbcopy not available")
	}
	cmd := exec.Command(path)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
