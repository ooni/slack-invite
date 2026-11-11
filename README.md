# slack-invite

A page where people ask to join OONI's Slack. It replaces slackin, which
invited people through Slack's undocumented `users.admin.invite` and a legacy
token, both of which Slack has been shutting down.

## How it works

1. Someone enters their email address, and optionally what brings them to
   OONI, at slack.ooni.org.
2. The service posts the request to an admin Slack channel through an
   incoming webhook.
3. An admin invites them from Slack (workspace menu → Invite people). Slack
   emails them an invitation for their address. To decline, do nothing.

Nothing is stored. Email and IP addresses go only to the Slack channel and
aren't logged. A hidden honeypot field and rate limits (3 requests per client
and 30 overall per hour) keep spam down.

## Configuration

From the environment:

| Variable | |
|---|---|
| `SLACK_WEBHOOK_URL` | Incoming webhook of the admin channel (`https://hooks.slack.com/services/...`) |
| `SLACK_WEBHOOK_URL_FILE` | Or a file holding it |
| `WORKSPACE_NAME` | Shown on the page and in requests (default `OONI`) |
| `CONTACT_EMAIL` | Shown as a fallback for people who can't use the form |
| `SLACK_SIGNIN_URL` | The workspace's URL (`https://<workspace>.slack.com/`), linked for members to sign in |
| `CLIENT_IP_HEADER` | Header the load balancer puts the client's address in (`X-Forwarded-For` behind an AWS load balancer; its last address is used). Only for rate limiting |
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
