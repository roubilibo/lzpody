# lzpody

A small native Podman terminal UI inspired by lazydocker.

The TUI talks to Podman's rootless **Libpod REST API** over the Unix socket.
It does not invoke Docker CLI or require a Docker compatibility layer.

## Features

- Native rootless Libpod API over the Podman Unix socket.
- Containers, Pods, Images, Volumes, and Networks views.
- Container CPU usage in resource rows, health in summaries, and port/forwarding details.
- Image rows show the image tag and size.
- Automatic refresh every two seconds.
- Start, stop, restart, and remove actions.
- Container logs with automatic refresh.
- Container and pod resource stats.
- Container stats with CPU/memory gauges and refresh history sparklines.
- Native inspect output for every resource type.
- Interactive container shell through the native `podman exec` command.
- Name/state/image filter.
- Lazydocker-style stacked resource panels, focused detail view, scrolling, and action menu.
- Python 3.11+ with no third-party runtime dependencies.

## Run

Make sure the rootless Podman socket is available:

```bash
systemctl --user enable --now podman.socket
./lzpody
```

Install to `~/.local/bin` with curl:

```bash
curl -fsSL https://raw.githubusercontent.com/roubilibo/lzpody/main/install.sh | bash
```

The installer is dependency-light: it downloads the TUI and wrapper, then
places them in `~/.local/bin`. Override the destination with `PREFIX` or
`BIN_DIR`, and pin a release with `LZPODY_VERSION`.

For a fork or another hosting location, override the source base URL with
`LZPODY_BASE_URL`.

If the socket was enabled before but its file is missing, recover it with:

```bash
systemctl --user restart podman.socket
```

The default socket is:

```text
$XDG_RUNTIME_DIR/podman/podman.sock
```

Override it when needed:

```bash
LZPODY_SOCKET=/path/to/podman.sock ./lzpody
```

## Keys

`↑/↓` or `j/k` select an item, `←/→` or `h/l` switch resource panels, and
`Tab` moves to the next panel. `1-5` focuses a resource panel. `Enter` moves
to the main detail view and `Esc` returns to the resource panels. In the main
view, `↑/↓` or `j/k` scroll, `PgUp/PgDn` scroll by a page, and `Home/End`
jump to the beginning or end. `Ctrl-U/Ctrl-D` also scroll by a page. `[`/`]`
switch detail tabs.

`x` or `?` opens the action menu. Use `↑/↓`, `j/k`, `Enter`, or `Space` to
choose an action and `Esc` or `q` to close it. Container shortcuts follow
lazydocker: `e` hides/shows stopped containers, `p` pauses/unpauses, `s`
stops, `r` restarts, `m` shows logs, `a` attaches, `E` opens a shell, and
`d` removes. `S` starts a stopped container, `i` opens config, `t` shows
stats, `K` kills, `/` filters, `F5` refreshes, and `q` quits.

Detail actions such as logs, config, environment, top, and stats focus the
detail panel automatically. Use `↑/↓` or `j/k` for line scrolling,
`PgUp/PgDn` or `Ctrl-U/Ctrl-D` for page scrolling, and `Home/End` to jump.

Stopping a container always asks for confirmation. The action menu also shows
the shortcut hint beside each container action.

The resource selector is displayed as five stacked panels on the left, while
the focused detail panel remains on the right, following lazydocker's layout
and navigation model.

## Omarchy appearance

The TUI reads the active Omarchy palette from
`~/.config/omarchy/themes/<active-theme>/colors.toml`. It uses the theme's
foreground, accent, selection, muted, green, red, and yellow colors, so a
theme change is reflected the next time the TUI starts.

## Development

```bash
python3 -m unittest discover -s tests -v
python3 -m py_compile lzpody.py
```
