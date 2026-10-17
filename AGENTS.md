# Working on QuickerConfigSwitch

Read `README.md` for user behavior, `docs/HANDOFF.md` for current state and
`docs/STAGES.md` for the active stage and its validation. `docs/ARCHITECTURE.md`
explains the design; where it still describes future work, follow the running
code and the handoff's explicit implementation status. Read `docs/CLIENTS.md`
for the Codex/ChatGPT desktop and Claude CLI/Desktop configuration boundaries.

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
- Direct endpoints must receive exact provider model IDs. Magpie's gateway can
  reverse aliases; a configuration-only adapter cannot. Preserve existing
  effort caps and reject unsupported selections rather than reporting success.
- Claude Desktop is a separate 3p deployment using its own user-data folder.
  Preserve official login data without claiming it is active in 3p. Keep the
  model compatibility rule in the adapter and expose ModelRefs to the UI.
- Relay listeners belong to a long-lived process. Preview before applying,
  use a session marker for cleanup, and release listeners/locks on every exit.
  An old session must not undo a newer switch.
- Tests use `internal/testenv`; every new Go test package needs
  `func TestMain(m *testing.M) { testenv.Main(m) }`. Use fake HTTP backends and
  temporary configuration, not actual accounts, startup entries or user homes.

## Validation and stages

Go 1.26 is selected from `go.mod` by `GOTOOLCHAIN=auto`; Ubuntu's packaged
Go 1.22 is enough to start that download. In test shells run `umask 022`
because the readonly-file fixtures depend on ordinary permissions.

## Cursor Cloud specific instructions

The Cloud Agent install script installs `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`, then runs `go mod download`. No daemon has to be running before development. `qcs web` and `go run ./internal/webui/internal/devserver` are started only when someone is looking at the UI. Node is already on the login `PATH` for the frontend tests. There is no `/workspace/.qcs-tools` tree on this environment.

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
Use `docs/ACCEPTANCE.md` for the remaining native-machine checks and evidence.
