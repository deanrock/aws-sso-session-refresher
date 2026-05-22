# aws-sso-session-refresher

Automatically refreshes AWS SSO session via Safari browser on macOS.

#### How it works

AWS SSO session is silently refreshed (`aws sso login`) every hour via a background Safari tab.

If user input is required (e.g. upstream SSO session requires re-login or approval), Safari tab (and window) is brought in the foreground.

#### Requirements
- macOS
- logged in to the upstream SSO provider in Safari
- configured AWS SSO and working AWS CLI

#### Installation

- Download .zip from a release.
- Run `xattr -c SSORefresher.app`, since app is not notarized.

If you want to auto-start app on login, you need to add it under:
`System Settings` -> `General` -> `Login Items & Extensions` -> `Open at Login` -> select app
