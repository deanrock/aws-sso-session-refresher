# aws-sso-session-refresher

Automatically refreshes AWS SSO session via Safari browser on macOS.

#### How it works

AWS SSO session is silently refreshed (`aws sso login`) every hour via a background Safari tab.

If user input is required (e.g. upstream SSO session requires re-login or approval), Safari tab (and window) is brought in the foreground.

#### Requirements
- macOS
- logged in to the upstream SSO provider in Safari
- configured AWS SSO and working AWS CLI
