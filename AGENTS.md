# AGENTS.md

Read [README.md](README.md) first. It covers the protocol, the two WS-Discovery flavors,
the `wsd` API, the `wsdc` CLI and the package layout — do not restate any of it here.

This file records only what the README does not: the commands, and the conventions for
changing the code.

## Commands

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .                      # must print nothing
go run ./bin/wsdc discover lo   # smoke-test the CLI
```

`.github/workflows/ci.yml` gates on the first four; the smoke test is run by hand,
since no runner has a device to answer a Probe. `-race` is not optional here: `Listen`
runs a goroutine per IP family, and both read paths unblock a blocked reader by moving
its socket deadline from another goroutine.

## Licence header on every new `.go` file

Copy the copyright line and the MIT SPDX line from an existing file — `wsd/device.go`
is the plain case. Six files carry an extra provenance line between the copyright and
the SPDX line, and which one depends on where the file lives:

| Path | Extra provenance line |
| --- | --- |
| `gosoap/*` | derives from `github.com/jfsmig/onvif`, MIT, © 2018 Yakovlev Dmitry, Zhorzh Palanjyan, Crazybber |
| `wsd/discover.go`, `wsd/ws-discovery.go` | derives from the ws-discovery project, © 2018 Palanjyan Zhorzhik |
| everything else | none |

**Keep the blank line between the notice and `package X`.** Without it Go takes the
licence as the package doc comment. The real package doc lives in `wsd/doc.go`; see
`wsd/device.go:4-5` for the spacing.

## Conventions

- **`bin/` is source, not build output.** `bin/wsdc/` holds the CLI. Do not add `bin/`
  to `.gitignore` the way most Go templates do — the built binary is ignored as `/wsdc`.

- **Comments explain why, and cite the spec.** The house style carries clause numbers:
  `SOAP-over-UDP 1.1 section 4`, `SOAP 1.2 Part 1 section 5.2.3`, `ONVIF Core section
  7.3.6`, `RFC 4122`. A comment restating what the next line does is out of place.

- **`wsd` must not depend on an ONVIF client type.** Discovery runs before any client
  exists, so `wsd.Device` is owned by `wsd`. `go list -deps ./wsd` must show no
  first-party package beyond `gosoap` and `wsd/transport`.

- **`cobra` stops at `bin/wsdc`.** The command tree, the exit statuses and the help text
  are the CLI's business. `wsd`, `wsd/transport` and `gosoap` must not import `cobra` or
  `pflag`: `go list -deps ./wsd ./gosoap | grep spf13` must print nothing. It is there for
  the per-verb flag sets and for `--help` answered as a success on stdout, not for
  testability: `run(ctx, args, stdout, stderr) int` is the seam the tests use, and it owes
  nothing to cobra.

- **Parsing paths drop, they do not fail or panic.** Datagrams are unauthenticated
  multicast from any host on the link. `etree` reports no error and leaves the root nil
  on non-XML input, so `documentRoot` returns nil and every caller checks it —
  dereferencing it would be a panic any host on the link could trigger.

- Prefer short methods or functions. Prefer comments of functions instead of comments
  of lines or blocks. Always comment in English.

- libraries in this repository should not `log.Print*` anything. If really necessary, any 
  debug trace should be emitted via a configurable logger. 
