# Working on QuickerConfigSwitch

Read `README.md` for user behavior, `docs/HANDOFF.md` for current state and
`docs/STAGES.md` for the active stage and its validation. `docs/ARCHITECTURE.md`
explains the design; where it still describes future work, follow the running
code and the handoff's explicit implementation status.

## Structure and conventions

- This project extracts Magpie's configuration-switching functionality.
  Before implementing a subsystem, inspect `yetone/magpie` for working code and
  tests. Prefer copying/adapting the small relevant implementation over writing
  another version. Keep its MIT copyright/license notice, update `NOTICE`, and
  record the upstream commit, paths and necessary local changes in
  `docs/UPSTREAM.md`. Do not import unrelated routing/account subsystems.
- Go 1.26; native JavaScript modules embedded from `internal/webui/assets`.
  No npm install or frontend build is required. Match the nearby Go/JS style,
  use `gofmt`, and put user-facing UI text in the existing Chinese/English i18n map.
- Agent adapters read configuration and produce `plan.Plan`; they never write
  configuration. `safekeep` owns snapshots, atomic writes, drift and restoration.
- Never write Codex `auth.json` or Claude login credentials. Keep unrelated
  settings, comments, BOM/CRLF and links; restore only values still owned by qcs.
- Relay listeners belong to a long-lived process. Preview before applying,
  use a session marker for cleanup, and release listeners/locks on every exit.
  An old session must not undo a newer switch.
- Tests use `internal/testenv`; every new Go test package needs
  `func TestMain(m *testing.M) { testenv.Main(m) }`. Use fake HTTP backends and
  temporary configuration, not actual accounts, startup entries or user homes.

## Validation and stages

On the prepared cloud machine source `/workspace/.qcs-tools/activate.sh` for
Go 1.26. Else use an installed Go matching `go.mod`. In test shells run
`umask 022` because the readonly-file fixtures depend on ordinary permissions.

```sh
go test -race -p 4 -tags nogui ./...
go vet -tags nogui ./...
node --test internal/webui/assets/*.test.mjs
make cli
git diff --check
```

Linux GUI builds use `make build` / `make test` (`gtk3` tag) and require
`libgtk-3-dev libwebkit2gtk-4.1-dev`. Cross-compiling `nogui` verifies compilation,
not native desktop behavior. Record platform-specific unrun checks explicitly.

Finish a coherent stage, run its relevant checks, update the handoff and stage
record, then commit that stage. Do not include half-finished later-stage work.
Use the existing isolated checkout; create another worktree only if requested.
Keep milestones, validation, limitations and next steps in repository docs so
another agent can resume without reading prior chat. Do not claim real-account
or real-desktop acceptance from fixture tests.
