// Command recap drafts and renders a team's weekly update from GitHub or GitLab
// activity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jalet/recap/internal/activity"
	"github.com/jalet/recap/internal/config"
	"github.com/jalet/recap/internal/issue"
	"github.com/jalet/recap/internal/render"
	"github.com/jalet/recap/internal/summarize"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: recap <command> [flags]

Commands:
  draft    collect GitHub or GitLab activity and write issues/<week>/index.md
  render   turn an issue into out/<week>/mattermost.md and email.html
  version  print the version

Run 'recap <command> -h' for the flags of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "draft":
		err = runDraft(ctx, os.Args[2:])
	case "render":
		err = runRender(os.Args[2:])
	case "version", "--version":
		fmt.Println("recap", version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runDraft(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("draft", flag.ExitOnError)
	cfgPath := fs.String("config", "recap.yaml", "config file")
	issuesDir := fs.String("issues", "issues", "folder holding one folder per week")
	weekFlag := fs.String("week", "", "ISO week to draft, e.g. 2026-W39 (default: the current week)")
	weeks := fs.Int("weeks", 0, "how many weeks of activity to include, ending with -week (default: window_weeks from config)")
	noAI := fs.Bool("no-ai", false, "write a skeleton with the raw activity instead of asking Claude")
	force := fs.Bool("force", false, "replace an existing index.md")
	footerPath := fs.String("footer", "footer.md", "links section appended below the draft (skipped if missing)")
	_ = fs.Parse(args) // ExitOnError: Parse exits instead of returning an error

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *weeks == 0 {
		*weeks = cfg.WindowWeeks
	}
	now := time.Now().UTC()
	week := issue.WeekOf(now)
	if *weekFlag != "" {
		if week, err = issue.ParseWeek(*weekFlag); err != nil {
			return err
		}
	}
	from, to := week.Window(*weeks)
	if to.After(now) {
		to = now
	}

	dir := issue.Dir(*issuesDir, week)
	indexPath := filepath.Join(dir, "index.md")
	if _, err := os.Stat(indexPath); err == nil && !*force {
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
	if !*noAI {
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
		body = issue.WithStats(body, stats.Line(*weeks))
	}
	footer, err := issue.ReadFooter(*footerPath)
	if err != nil {
		return err
	}
	body = issue.WithFooter(body, footer)
	if err := issue.WriteNew(indexPath, fm.Render()+body, *force); err != nil {
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

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	issuesDir := fs.String("issues", "issues", "folder holding one folder per week")
	outDir := fs.String("out", "out", "folder for rendered output")
	weekFlag := fs.String("week", "", "ISO week to render (default: the newest folder under -issues)")
	noClipboard := fs.Bool("no-clipboard", false, "do not copy the Mattermost text to the clipboard")
	_ = fs.Parse(args) // ExitOnError: Parse exits instead of returning an error

	var week issue.Week
	var err error
	if *weekFlag != "" {
		week, err = issue.ParseWeek(*weekFlag)
	} else {
		week, err = issue.Latest(*issuesDir)
	}
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(issue.Dir(*issuesDir, week))
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

	target := filepath.Join(*outDir, week.String())
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	mmPath := filepath.Join(target, "mattermost.md")
	emailPath := filepath.Join(target, "email.html")
	if err := os.WriteFile(mmPath, []byte(out.Mattermost), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(emailPath, []byte(out.Email), 0o644); err != nil {
		return err
	}

	fmt.Printf("Mattermost: %s\nEmail:      %s (open in a browser, select all, paste into Outlook)\n", mmPath, emailPath)
	if !*noClipboard {
		if err := copyToClipboard(out.Mattermost); err == nil {
			fmt.Println("Mattermost text copied to the clipboard.")
		}
	}
	if len(out.Media) > 0 {
		fmt.Println("\nAttach these files to the Mattermost post (and videos to the email):")
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
