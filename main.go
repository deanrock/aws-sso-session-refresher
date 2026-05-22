package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/energye/systray"
)

type appStatus struct {
	mu          sync.Mutex
	lastRefresh time.Time
	identity    string
}

var status = &appStatus{}
var menuUpdate = make(chan struct{}, 1)

func onReady(ctx context.Context) {
	systray.SetIcon(iconData)
	systray.SetOnRClick(func(menu systray.IMenu) {
		menu.ShowMenu()
	})

	mRefresh := systray.AddMenuItem("Not yet refreshed", "")
	mRefresh.Disable()
	mIdentity := systray.AddMenuItem("", "")
	mIdentity.Disable()
	mIdentity.Hide()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit")
	mQuit.Click(func() { systray.Quit() })

	update := func() {
		status.mu.Lock()
		lr := status.lastRefresh
		id := status.identity
		status.mu.Unlock()

		if lr.IsZero() {
			mRefresh.SetTitle("Not yet refreshed")
		} else {
			mRefresh.SetTitle("Last refresh: " + formatAgo(lr))
		}

		if id != "" {
			mIdentity.SetTitle(id)
			mIdentity.Show()
		}
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				update()
			case <-menuUpdate:
				update()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func onExit(cancel context.CancelFunc) {
	cancel()
}

func refresher(ctx context.Context) {
	// Initialize to one hour ago so the first check triggers immediately.
	status.mu.Lock()
	status.lastRefresh = time.Now().Add(-time.Hour)
	status.mu.Unlock()

	for {
		status.mu.Lock()
		lr := status.lastRefresh
		status.mu.Unlock()

		if time.Since(lr) >= time.Hour {
			if err := refreshToken(ctx); err != nil {
				slog.Error("refreshing token failed", slog.String("err", err.Error()))
			} else {
				status.mu.Lock()
				status.lastRefresh = time.Now()
				status.mu.Unlock()

				notifyMenu()

				go func() {
					if id := fetchIdentity(); id != "" {
						status.mu.Lock()
						status.identity = id
						status.mu.Unlock()
						notifyMenu()
					}
				}()
			}
		}

		// time.Sleep is suspended during macOS sleep; using a short timer
		// ensures we re-check promptly after the machine wakes.
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func notifyMenu() {
	select {
	case menuUpdate <- struct{}{}:
	default:
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	go refresher(ctx)

	systray.Run(func() { onReady(ctx) }, func() { onExit(cancel) })
}

func refreshToken(ctx context.Context) error {
	slog.Info("refreshing token...")

	cmd := exec.Command("aws", "sso", "login", "--no-browser")
	stdout, _ := cmd.StdoutPipe()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	go streamReader(stderr, "err", nil)

	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		if cmd.Process != nil && cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
			if err := cmd.Process.Kill(); err != nil {
				slog.Error("killing process failed", slog.String("err", err.Error()))
			}
		}
	}()

	authorizationUrl, err := print(stdout)
	if err != nil {
		return err
	}

	parsed, err := url.Parse(authorizationUrl)
	if err != nil {
		return err
	}
	redirectURI, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil {
		return err
	}
	cliOrigin := redirectURI.Scheme + "://" + redirectURI.Host

	file, err := os.CreateTemp(os.TempDir(), "sso-refresher")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if err := os.WriteFile(file.Name(), []byte(script(authorizationUrl)), 0600); err != nil {
		return err
	}

	osaCmd := exec.Command("osascript", file.Name())
	if err := osaCmd.Run(); err != nil {
		return err
	}

	// Capture the Safari window ID immediately after opening the tab, before any redirect.
	winIDOut, _ := exec.Command("osascript", "-e", `tell application "Safari" to return id of front window as string`).Output()
	safariWinID := strings.TrimSpace(string(winIDOut))

	// If not authorized within 10s, bring the Safari window to the foreground.
	focusCtx, focusCancel := context.WithCancel(ctx)
	defer focusCancel()
	go func() {
		select {
		case <-time.After(10 * time.Second):
			focusScript := fmt.Sprintf(`tell application "Safari"
	activate
	repeat with w in windows
		if (id of w as string) is "%s" then
			set index of w to 1
			return
		end if
	end repeat
end tell`, safariWinID)
			exec.Command("osascript", "-e", focusScript).Run()
		case <-focusCtx.Done():
		}
	}()

	if err := cmd.Wait(); err != nil {
		return err
	}

	// Close only the AWS CLI callback tab (matched by the exact origin from redirect_uri).
	closeScript := fmt.Sprintf(`tell application "Safari"
		set tabsToClose to {}
		repeat with w in windows
			repeat with t in tabs of w
				if URL of t starts with "%s" then
					set end of tabsToClose to t
				end if
			end repeat
		end repeat
		repeat with t in tabsToClose
			close t
		end repeat
	end tell`, cliOrigin)
	exec.Command("osascript", "-e", closeScript).Run()

	slog.Info("token refreshed")

	return nil
}

type callerIdentity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
}

func fetchIdentity() string {
	out, err := exec.Command("aws", "sts", "get-caller-identity").Output()
	if err != nil {
		return ""
	}
	var id callerIdentity
	if err := json.Unmarshal(out, &id); err != nil {
		return ""
	}
	// arn:aws:sts::ACCOUNT:assumed-role/ROLE/SESSION  →  ACCOUNT › ROLE/SESSION
	// arn:aws:iam::ACCOUNT:user/USER                  →  ACCOUNT › USER
	parts := strings.SplitN(id.Arn, ":", 6)
	if len(parts) != 6 {
		return id.Arn
	}
	resource := parts[5]
	if idx := strings.Index(resource, "/"); idx >= 0 {
		resource = resource[idx+1:]
	}
	return id.Account + " › " + resource
}

func formatAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	default:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	}
}

func script(url string) string {
	return fmt.Sprintf(`set myURLS to "%s"
set {saveTID, AppleScript's text item delimiters} to {AppleScript's text item delimiters, {linefeed}}
set myURLS to text items of myURLS
set AppleScript's text item delimiters to saveTID
repeat with oneURL in myURLS
	tell application "Safari"
		tell front window
			make new tab at end of tabs with properties {URL:oneURL}
		end tell
	end tell
end repeat
`, url)
}

func print(stdout io.ReadCloser) (string, error) {
	r := bufio.NewReader(stdout)

	for {
		buf, _, err := r.ReadLine()
		if err != nil {
			return "", err
		}
		s := string(buf)
		fmt.Printf("line: %s err %s\n", string(buf), err)

		if strings.Contains(s, "https://oidc.") {
			go streamReader(stdout, "out", nil)
			return s, nil
		}
	}
}

func streamReader(rc io.ReadCloser, name string, handler *func(string)) error {
	r := bufio.NewReader(rc)

	for {
		buf, _, err := r.ReadLine()
		if err != nil {
			return err
		}

		if handler != nil {
			(*handler)(string(buf))
		}

		slog.Info("stream reader", slog.String("name", name), slog.String("line", string(buf)))
	}
}
