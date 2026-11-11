# slack-invite

A page where people ask to join OONI's Slack. It replaces slackin, which
invited people through Slack's undocumented `users.admin.invite` and a legacy
token, both of which Slack has been shutting down.

## How it works

1. Someone enters their email address at slack.ooni.org (and, if `ASK_NOTE`
   is set, what brings them to OONI).
2. The service posts the request to an admin Slack channel through an
   incoming webhook.
3. An admin invites them from Slack (workspace menu → Invite people). Slack
   emails them an invitation for their address. To decline, do nothing.

Nothing is stored or logged. The service sends Slack only the email
address (Slack sees visitors' IP addresses anyway once they join). For rate
limiting it counts IP addresses in memory as HMACs, keyed with a random key
made for each window; at the end of the window the counts and the key are
thrown away together, so they can't be matched to addresses afterwards. A hidden
honeypot field and rate limits (by default 3 requests per IPv4 address or
IPv6 /64, and 30 overall, per hour) keep spam down, and forms posted from other sites
are refused. What visitors type reaches Slack as plain text, shown exactly
as typed, without control or formatting characters.

## Configuration

From the environment:

| Variable | |
|---|---|
| `SLACK_WEBHOOK_URL` | Incoming webhook of the admin channel (`https://hooks.slack.com/services/...`) |
| `SLACK_WEBHOOK_URL_FILE` | Or a file holding it |
| `WORKSPACE_NAME` | Shown on the page and in requests (default `OONI`) |
| `CONTACT_EMAIL` | Shown as a fallback for people who can't use the form |
| `SLACK_SIGNIN_URL` | The workspace's URL (`https://<workspace>.slack.com/`), linked for members to sign in |
| `ASK_NOTE` | `true` to also ask what brings people to the workspace and pass it on. Off by default: only the email address is asked for |
| `CLIENT_IP_HEADER` | Header the load balancer puts the client's address in (`X-Forwarded-For` behind an AWS load balancer; its last address is used). Only for rate limiting |
| `RATE_LIMIT_PER_CLIENT` | Requests allowed per IPv4 address or IPv6 /64 in each window (default `3`) |
| `RATE_LIMIT_TOTAL` | Requests allowed overall in each window (default `30`) |
| `RATE_LIMIT_WINDOW` | The window, e.g. `1h` or `30m`, at least `1m` (default `1h`) |
| `LISTEN_ADDR` | Address to listen on (default `:3000`; the image sets `:80`) |

To make the webhook: create a Slack app in the workspace, turn on Incoming
Webhooks, and add one for the admin channel.

## Building and deploying

`make docker-build` builds the image, running the Go tests as part of the
build. `make docker-smoketest` starts it and checks it answers.

It runs as an ECS service set up in ooni/devops with the `ooniapi_service`
and `ooniapi_service_deployer` modules. The deployer's CodeBuild project runs
`buildspec.yml` on every push to `main`: it builds, smoketests and pushes
`ooni/slack-invite:<date>-<sha>`, and ECS deploys it.

The image is Debian stable (`trixie-slim`) with `ca-certificates` from apt,
for HTTPS to Slack. Rebuilding picks up their updates.

The page looks like slackin's did. `assets/org-logo.png` is the workspace's
icon and `assets/slack.svg` Slack's logo; the service serves them itself, so
the page loads nothing from other sites.

`/badge.svg` is a "slack | join" badge in place of slackin's, which READMEs
embed. Unlike slackin's, it has no member count.

## License

BSD 3-Clause (LICENSE). The page's look and the badge are derived from
[slackin-extended](https://github.com/emedvedev/slackin-extended) and
[slackin](https://github.com/rauchg/slackin), under the MIT license, and
the Slack logo belongs to Slack: see NOTICE.
