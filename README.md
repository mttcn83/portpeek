# portpeek

See what's using your ports, and stop it. Linux only (reads `/proc`, uses pidfds:
kernel 5.3+). Go, standard library only, one static binary.

```
  ██████╗  ██████╗ ██████╗ ████████╗██████╗ ███████╗███████╗██╗  ██╗
  ██╔══██╗██╔═══██╗██╔══██╗╚══██╔══╝██╔══██╗██╔════╝██╔════╝██║ ██╔╝
  ██████╔╝██║ ● ██║██████╔╝   ██║   ██████╔╝█████╗  █████╗  █████╔╝
  ██╔═══╝ ██║   ██║██╔══██╗   ██║   ██╔═══╝ ██╔══╝  ██╔══╝  ██╔═██╗
  ██║     ╚██████╔╝██║  ██║   ██║   ██║     ███████╗███████╗██║  ██╗
  ╚═╝      ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚═╝     ╚══════╝╚══════╝╚═╝  ╚═╝

  4 listening · 1 open to the network

OPEN TO THE NETWORK  other machines can reach these
●   8080  python    6132    -m http.server 8080  in ~/site               up 3h

ONLY THIS COMPUTER  not reachable from other machines
○   3000  node      4821    server.js  in ~/projects/app                 up 2h
○   5432  postgres  1204    -D /var/lib/postgres/data                    up 1d

FREE FOR LOCALHOST  first open port from each usual dev start
  3001   5174   8000   8080
```

```
portpeek                 what's listening right now (add -u for UDP)
portpeek 3000            who has port 3000? command, cwd, uptime, reach
portpeek kill 3000       SIGTERM it, asks first (-y skips, -f sends SIGKILL)
portpeek free [PORT]     print a port that's free for localhost (default: from 3000)
portpeek wait 5432       block until something listens on 5432 (--free: until released;
                         -t 30s timeout, default 60s, 0 = forever). Exit 0 reached, 1 timeout
portpeek --json          machine-readable (also works with PORT)
```

- **Open to the network** = bound to a non-loopback address; **only this computer** =
  loopback only. A firewall may still block "open" ports.
- Rows show the short command and the project folder it runs in. For owners you can't
  see, well-known ports get a plainly-labelled guess ("probably SSH").
- Owners of other users' processes are hidden unless you run with `sudo`. portpeek
  never elevates on its own.
- `kill` pins the process with a pidfd and re-checks it still owns the port before
  signalling, so a recycled PID is never hit. It says so if something respawns the port.
- Exit status: `0` ok, `1` nothing found / kill failed, `2` usage, `3` permission denied.
- The banner is ANSI Shadow lettering with an eye for the O. It appears only on a UTF-8
  terminal at least 70 columns wide, never in pipes. Gradient is 24-bit where the terminal
  supports it, 256-color otherwise. (Block and box-drawing characters stay solid in
  terminals with extra line spacing; thin ASCII strokes like `#` fall apart.)
- `free` binds both 127.0.0.1 and ::1 to be sure a port is really free, so
  `vite --port $(portpeek free)` just works.
- Honors `NO_COLOR`, `TERM=dumb`, and `COLUMNS`.

## Build / install / uninstall

```
go install github.com/mttcn83/portpeek@latest      # or build by hand:
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o portpeek .
install -Dm755 portpeek ~/.local/bin/portpeek
rm ~/.local/bin/portpeek                           # uninstall
```

## Tests

```
go test ./...
```

## Security notes

- portpeek only reads `/proc` and sends signals; it makes no network connections and
  needs no privileges. Don't install it setuid. `kill` can only signal what the kernel
  lets your user signal, and refuses PID 1 and itself.
- Process names, command lines and folders belong to whoever runs the process, so they
  are stripped of control characters before printing (a hostile process can't draw on
  your terminal, even when you run `sudo portpeek`).
- Command lines can contain secrets (`--password=...`, tokens). Check the output before
  pasting it into an issue or a screenshot; `--json` includes the full command.
- `free` and the "free for localhost" suggestions test ports by binding them for a
  moment on 127.0.0.1 and ::1, so another program can still grab the port afterwards.
- Found a vulnerability? Please report it privately through GitHub's "Report a
  vulnerability" button on the Security tab rather than a public issue.

## License

MIT, see [LICENSE](LICENSE).
