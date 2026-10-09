# recap

A team's weekly update to the rest of the organization, drafted from what the team
actually shipped: a headline, a short summary, milestones, toil, one demo, and a
standing links footer.

`recap draft` collects the week's merged pull or merge requests and direct commits from
GitHub or GitLab, and has Claude draft `issues/<week>/index.md`. `recap render` turns
the edited issue into a Mattermost post and an email you can paste into Outlook.

## Install

Download the binary for your platform from the
[releases](https://github.com/aldersfors/recap/releases) page, or build from source:

```sh
gh release download --repo aldersfors/recap --pattern '*darwin_arm64*'
# or
git clone git@github.com:aldersfors/recap.git && cd recap && mise run build   # bin/recap
```

## Set up

```sh
cp recap.example.yaml recap.yaml   # team, forge, repos, filters, model
cp footer.example.md footer.md     # links and self-service info under every issue
```

`recap.yaml`, `footer.md` and everything under `issues/` are gitignored: they hold your
team's details and activity, not the tool's.

## Weekly workflow

```sh
mise run draft                 # collect this week's activity, write issues/<week>/index.md
$EDITOR issues/2026-W39/index.md
cp ~/Desktop/demo.png issues/2026-W39/media/   # reference it as ![caption](media/demo.png)
mise run render                # write out/<week>/mattermost.md, email.html and email.eml
```

1. **Draft.** `recap draft` collects merged PRs, and commits pushed straight to the
   default branch, from the repos in `recap.yaml`. It saves them to
   `issues/<week>/activity.json` and asks the model for a draft in `issues/<week>/index.md`.
   The model sees the activity grouped by theme, largest first: by the epic each
   referenced `github.tickets_repo` ticket rolls up to, then untracked work by commit
   scope (`feat(<scope>): ...`). Linking PRs to tickets keeps related work together.
   The draft has a `##` headline and an executive summary, then `### Milestones`
   (finished epic work: items that reference a ticket in `github.tickets_repo`),
   `### Toil` (smaller upkeep) and `### Demo` (the week's biggest result, with a
   `<!-- MEDIA: ... -->` placeholder). `footer.md` is appended below a `---` rule:
   keep the standing links and self-service information there.
2. **Edit.** Check the draft against `activity.json`, tighten the wording, and replace each
   MEDIA comment with an image or video line such as `![New login page](media/login.png)`.
3. **Render and send.** `recap render` then covers both channels:
   - **Mattermost**: the text is on the clipboard (or in `out/<week>/mattermost.md`).
     Paste it into the post and attach the media files it lists.
   - **Outlook, draft**: open `out/<week>/email.eml`. It opens as an unsent message
     with the images inline and the videos attached; address it and send.
   - **Outlook, paste**: or open `out/<week>/email.html` in a browser (from disk, not
     through a local web server), select all, copy, and paste into a new message. The
     images are embedded in the page, so they come along. Attach videos yourself.

   Mail clients cannot play video, so a video shows its sibling `.gif` when there is
   one (`demo.mp4` + `demo.gif`), otherwise a caption.

`render` warns about leftover `TODO`s, unreplaced MEDIA comments, missing media files,
em-dashes, lines over 80 columns and missing Milestones, Toil or Demo headings.

## Commands

| Command | Flags |
|---------|-------|
| `recap draft` | `--week 2026-W39` (default: current ISO week), `--weeks N` (default: `window_weeks`), `--no-ai` (skeleton with the raw activity), `--force` (replace an existing index.md), `--footer` (default `footer.md`; skipped if missing), `--config`, `--issues` |
| `recap render` | `--week 2026-W39` (default: newest week folder), `--no-clipboard`, `--issues`, `--out` |
| `recap version` | prints the version (also `recap --version`) |

Flags take two dashes (`--week`); the single-dash form (`-week`) is rejected.
`recap <command> --help` lists every flag with its default.

The window runs from Monday 00:00 UTC of the first week to the end of the drafted week,
or to now if that week is still in progress.

## Configuration

`recap.yaml` sets which repos are scanned and which backend drafts the update:

- `team.name`, `team.audience`: who writes the update and who reads it. Both go into
  the drafting prompt; `name` also titles each issue (`Platform recap 2026-W39`).
- `forge`: `github` (default) or `gitlab`. One run scans one forge; a GitLab team uses
  its own config file, e.g. `recap draft --config gitlab.yaml`.
- `github.repos`: the explicit repo list inside `github.org`.
- `github.topics`: optional. Every non-archived org repo tagged with one of these topics
  is added to the list.
- `github.exclude_bots`: skip every author whose login ends in `[bot]`.
- `github.exclude_authors`: further logins to skip.
- `github.include_teams`, `github.include_authors`: optional. When either is set, only
  items by members of these org teams (by slug) or by these logins are kept. Commits
  whose author has no linked GitHub account are dropped. Excludes still apply on top.
- `github.tickets_repo`: optional. Issues in this org repo closed as completed in the
  window are counted into an italic line under the summary, such as
  `*Closed this week: 12 tickets, 2 epics.*`. Epics are issues of type `Epic`. Only issues
  assigned to a member of `include_teams` or `include_authors` count, so it needs one of
  them set; unassigned issues never count. The tool counts; the
  model never sees or writes the numbers. GitHub only.
- `gitlab.base_url` (default `https://gitlab.com`), `gitlab.group` (full path),
  `gitlab.projects` and `gitlab.topics`: projects are paths relative to the group.
- `gitlab.include_groups`, `gitlab.include_authors`: the GitLab form of the include
  filter. Groups are relative to `gitlab.group` and count direct and subgroup
  members, not members of parent groups. GitLab
  reports commits by email only, so direct commits match only an email in
  `include_authors`. `exclude_authors` (case-insensitive) and `exclude_bots` work as on GitHub; bots are
  project and group access-token users.
- `ai.provider`: `anthropic`, `bedrock` or `converse`. Switch back by changing this one value.
  `bedrock` serves Claude only and needs the account to have accepted the Anthropic
  model agreement in AWS Marketplace. `converse` reaches any Bedrock text model, for
  example `zai.glm-5`, through the Converse API; many of those need no Marketplace agreement.
- `ai.anthropic.inference_geo`: optional `inference_geo` sent to the Claude API. Empty
  uses the API default (global). The draft step prints where inference ran.

## Credentials

Nothing secret goes in the config.

| Service | Source |
|---------|--------|
| GitHub | `GITHUB_TOKEN`, else `gh auth token` |
| Bedrock, Converse | AWS credential chain, e.g. `granted`/SSO profile, or `ai.bedrock.profile` / `ai.converse.profile` |
| Anthropic API | `ANTHROPIC_API_KEY`, or an `ant auth login` profile |
| GitLab | `GITLAB_TOKEN`, else `glab config get token --host <host>` (scope `read_api`) |

## Data handling

The draft step sends PR titles, bodies (each capped at 1500 characters) and commit
messages to the model. They can contain names and internal details. Author names
are personal data under GDPR Article 4(1) (2018-05-25). No author is sent to the model,
and `Signed-off-by`-style trailers are stripped from bodies first. `activity.json` keeps
author logins so the draft can be checked against it; GitLab commit emails are dropped
before it is written.

- **Bedrock** uses the in-region `bedrock-mantle` endpoint in eu-north-1
  (Stockholm), so the model runs in the EU region.
- **Converse** calls `bedrock-runtime` in the configured region. With a model offered
  on demand there (check `aws bedrock list-foundation-models`), inference runs in that
  region; a model reachable only through a cross-region inference profile can run in
  other regions of that geography. AWS hosts the model, so a non-Anthropic model such
  as `zai.glm-5` does not send the data to its model provider. VERIFY WITH LEGAL
  COUNSEL before using a new model with real data.
- **Anthropic API** sends the data to Anthropic, a third-party processor. With
  `inference_geo: global` the model may run outside the EU, which is a transfer under GDPR
  Chapter V (Articles 44-46, 2018-05-25). Before using it with real data, complete a vendor
  assessment and have a DPA with a valid transfer mechanism (for example SCCs, GDPR
  Article 46(2)(c)) in place, per GDPR Article 28 (2018-05-25) and NIS2 Article 21(2)(d)
  (2024-10-17). VERIFY WITH LEGAL COUNSEL.

> This document provides technical guidance based on published regulatory frameworks. It
> does not constitute legal advice. Consult qualified legal counsel for compliance
> decisions.

## Development

```sh
mise run test     # unit tests; the GitHub and GitLab clients run against fake API servers
mise run lint
mise run release-check   # goreleaser build of all platforms into dist/, no publish
go test ./internal/render -update   # rewrite golden files after an intended output change
```

Layout: `cmd/recap` (CLI), `internal/config`, `internal/activity` (GitHub, GitLab),
`internal/summarize` (prompt in `prompt.md`, Anthropic, Bedrock and Converse providers),
`internal/issue` (ISO weeks, front matter), `internal/render`.

## CI and releases

Every pull request and push to `main` runs `.github/workflows/ci.yml`: `mise run ci`
(tests and lint), `goreleaser check` and a snapshot build of all platforms.

Releases come from [release-please](https://github.com/googleapis/release-please).
Commits follow [Conventional Commits](https://www.conventionalcommits.org), and on each
push to `main` release-please updates an open release PR with the next version and
`CHANGELOG.md`. Before 1.0, `feat` and breaking changes bump the minor version and
`fix` bumps the patch. Merging the release PR tags `vX.Y.Z`, creates the GitHub release
and runs goreleaser to attach the archives and `checksums.txt`. Never tag by hand.

release-please acts as the release GitHub App, so its PRs run CI like any other PR.
The release workflow reads the secrets `RELEASE_APP_CLIENT_ID` and
`RELEASE_APP_PRIVATE_KEY` (repo or org level), and the app must be installed on this
repo with read and write access to contents and pull requests.

## License

[Apache-2.0](LICENSE).
