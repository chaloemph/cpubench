# Security

cpubench is a lab tool. Run the hub, the nodes and the web target on a test
network you control, never on the internet.

## Trust model

| Part | What it trusts | What to do |
|---|---|---|
| `hub` | Anyone who knows the token | Always start it with `--token` (or `$CPUBENCH_TOKEN`). Without a token anyone who can reach port 8090 can upload results and press the run buttons. |
| hub ↔ node | Plain HTTP, no TLS | The token travels in clear text (Bearer for the API, Basic auth for the buttons). Keep the hub on a private network or put it behind a TLS reverse proxy. |
| `node` | The hub it is pointed at | A node only runs the built-in job types (`auto` profiles, `web serve`, `web load`). It never runs shell commands from the hub, but a hub can tell it to send load to any URL. Point nodes only at your own hub. |
| BMC password | The machine it is set on | `$CPUBENCH_BMC_PASSWORD` stays on the node and is never sent to the hub. Keep it in a file with mode 600 (see `deploy/systemd/`). |
| `web serve` | Nobody | It is a benchmark target with no authentication. Stop it when the test ends. |
| `startup --cmd` | The local user | It runs the command you give it through `sh -c`, as the user who runs cpubench. |

Result files and reports contain hostnames, CPU models, BIOS versions, PCI
devices, disk models and BMC addresses. Read them before you share them, and use
`cpubench compare --for <label>` to make a copy for one vendor (see README).

## Reporting a vulnerability

Please open a private report through GitHub's **Security → Report a
vulnerability** on this repository instead of a public issue.
