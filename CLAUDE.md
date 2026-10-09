# recap

A team's weekly update: `issues/<ISO week>/index.md` plus `media/`, drafted and rendered
by the Go CLI in `cmd/recap`. See README.md for the workflow.

- Tasks: `mise run test`, `mise run lint`, `mise run draft`, `mise run render`,
  `mise run release-check`.
- The drafting prompt lives in `internal/summarize/prompt.md`; `{{team}}`, `{{audience}}`
  and `{{ticket}}` are filled from the config. Render output is pinned by golden files in
  `internal/render/testdata`; update them with `go test ./internal/render -update`.
- Never use the em-dash in drafts, code or docs. `summarize.Sanitize` strips it from model output.
- Private repo `aldersfors/recap`, shared outside the original org. `recap.yaml`, `footer.md`
  and `issues/*` are gitignored and must stay out of commits: they hold one team's
  repos, hostnames and activity. Keep test fixtures on neutral names (`acme`, `alice`).
- Releases: merge the release-please PR; `.github/workflows/release.yml` tags `vX.Y.Z` and
  runs goreleaser (`.goreleaser.yaml`). Never tag by hand. Commit messages drive the
  version, so keep them Conventional Commits.
