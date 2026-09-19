# lzpody

A small native Podman terminal UI inspired by lazydocker.

The TUI talks to Podman's rootless **Libpod REST API** over the Unix socket.
It does not invoke Docker CLI or require a Docker compatibility layer.

## preview
<img width="1920" height="1080" alt="image" src="https://github.com/user-attachments/assets/8edb4137-0715-4503-b35b-6286a63a96ad" />

## Features

- Native rootless Libpod API over the Podman Unix socket.
- Containers, Pods, Images, Volumes, and Networks views.
- Lazydocker-style stacked resource panels, focused detail view.
- A single Go binary with no runtime dependencies beyond Podman and a Unix socket.

## Run

Make sure the rootless Podman socket is available:

```bash
systemctl --user enable --now podman.socket
./lzpody
```

From a source checkout, `./lzpody` uses `go run`; a release install executes
the downloaded binary directly.

Install to `~/.local/bin` with curl:

```bash
curl -fsSL https://raw.githubusercontent.com/roubilibo/lzpody/master/install.sh | bash
```

The installer is dependency-light: it downloads the platform binary and
places it in `~/.local/bin`. Override the destination with `PREFIX` or
`BIN_DIR`, and pin a release with `LZPODY_VERSION`.

For a fork or another release host, override the binary URL with
`LZPODY_BINARY_URL` or its release directory with `LZPODY_RELEASE_BASE_URL`.
The installer supports Linux `amd64`, `arm64`, and `armv7` builds.

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

# lzpody — Keyboard Shortcuts

## 1. Navigation

| Shortcut | Action |
|---|---|
| `↑` / `↓` | Select previous / next item |
| `j` / `k` | Select next / previous item |
| `←` / `→` | Switch resource panels |
| `h` / `l` | Switch resource panels |
| `Tab` | Move to the next panel |
| `1` – `5` | Focus a specific resource panel |
| `Enter` | Open the main detail view |
| `Esc` | Return to resource panels |

## 2. Detail View

| Shortcut | Action |
|---|---|
| `↑` / `↓` | Scroll up / down |
| `j` / `k` | Scroll down / up |
| `PgUp` / `PgDn` | Scroll one page up / down |
| `Ctrl-U` / `Ctrl-D` | Scroll one page up / down |
| `Home` | Jump to the beginning |
| `End` | Jump to the end |
| `[` / `]` | Switch detail tabs |

## 3. Action Menu

| Shortcut | Action |
|---|---|
| `x` / `?` | Open action menu |
| `↑` / `↓` | Navigate actions |
| `j` / `k` | Navigate actions |
| `Enter` / `Space` | Select an action |
| `Esc` / `q` | Close action menu |

## 4. Container Actions

Container shortcuts follow LazyDocker conventions.

| Shortcut | Action | Description |
|---|---|---|
| `e` | Toggle Stopped | Show / hide stopped containers |
| `p` | Pause / Unpause | Pause or resume a container |
| `s` | Stop | Stop a running container |
| `S` | Start | Start a stopped container |
| `r` | Restart | Restart a container |
| `m` | Logs | Open container logs |
| `a` | Attach | Attach to a container |
| `E` | Shell | Open a shell inside a container |
| `d` | Remove | Remove a container |
| `i` | Config | Show container configuration |
| `t` | Stats | Show container statistics |
| `K` | Kill | Forcefully terminate a container |

## 5. General Shortcuts

| Shortcut | Action |
|---|---|
| `/` | Filter items |
| `F5` | Refresh resources |
| `q` | Quit application |

---

**Note:** Detail actions such as Logs, Config, Environment, Top, and Stats automatically focus the detail panel. All Detail View navigation shortcuts apply there as well.

## Omarchy appearance

The TUI reads the active Omarchy palette from
`~/.config/omarchy/themes/<active-theme>/colors.toml` and applies the
foreground, accent, selection, muted, green, red, and yellow colors to the
panels, focus state, actions, and status line. It falls back to the bundled
Akane-compatible palette when Omarchy is unavailable.

## Development

The Go TUI is built around Bubble Tea's model/update/view lifecycle. Terminal
size and resize events come from Bubble Tea, styling is rendered with Lip Gloss,
and Podman requests plus external commands run as Bubble Tea commands and return
messages to `Update`.

```bash
go test ./...
go build -o lzpody-bin .
./lzpody-bin --help
```

Build release assets with names such as `lzpody-linux-amd64`:

```bash
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o lzpody-linux-amd64 .
```

Pushing a `v*` tag runs the release workflow and publishes the three Linux
assets consumed by `install.sh`.

## License

This project is licensed under the [MIT License](LICENSE).
