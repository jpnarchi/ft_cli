// ft: a small command-line tool for your own Forethought account.
//
//	ft login     authenticate with your email + password, cache tokens
//	ft whoami    show the signed-in user and org
//	ft token     print the current ID token (for scripts / the MCP server)
//	ft get PATH  read-only GET of any dashboard-api path
//
// Reads are plain GETs against the dashboard API (FT_API_BASE). Writes only touch
// a draft and need --apply; publish and channel on/off need --confirm (see
// SPEC.md). `ft login` talks to AWS Cognito to get your tokens.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	c, err := loadCreds()
	if err != nil {
		fail(err)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "login":
		cmdLogin(c)
	case "whoami":
		cmdWhoami(c)
	case "token":
		cmdToken(c)
	case "get":
		if len(args) < 1 {
			fail(fmt.Errorf("usage: ft get /some/api/path"))
		}
		cmdGet(c, args[0], parseOpts(args[1:]).has("show-secrets"))
	case "agents", "ls":
		cmdAgents(c, dropFirst(args, "list"))
	case "agent":
		sub, rest := subcmd(args, "get|versions|set-policy|set-intent|set-setting")
		o := parseOpts(rest)
		switch sub {
		case "get":
			cmdAgentGet(c, o)
		case "versions":
			cmdAgentVersions(c, o)
		case "set-policy":
			cmdSetPolicy(c, o)
		case "set-intent":
			cmdSetIntent(c, o)
		case "set-setting":
			cmdSetSetting(c, o)
		}
	case "actions":
		cmdActionsList(c, parseOpts(dropFirst(args, "list")))
	case "action":
		sub, rest := subcmd(args, "get|create|update")
		o := parseOpts(rest)
		switch sub {
		case "get":
			cmdActionGet(c, o)
		case "create":
			cmdActionCreate(c, o)
		case "update":
			cmdActionUpdate(c, o)
		}
	case "variables", "vars":
		cmdChipsList(c, parseOpts(dropFirst(args, "list")), "var")
	case "forms":
		cmdChipsList(c, parseOpts(dropFirst(args, "list")), "form")
	case "chips":
		cmdChipsList(c, parseOpts(dropFirst(args, "list")), "")
	case "search-sources":
		sub, rest := subcmd(args, "get|set")
		if sub == "get" {
			cmdSearchSourcesGet(c, parseOpts(rest))
		} else {
			cmdSearchSourcesSet(c, parseOpts(rest))
		}
	case "logs":
		sub, rest := subcmd(args, "get|list")
		if sub == "get" {
			cmdLogsGet(c, parseOpts(rest))
		} else {
			cmdLogsList(c, parseOpts(rest))
		}
	case "metrics":
		cmdMetrics(c, parseOpts(args))
	case "version":
		sub, rest := subcmd(args, "save|restore")
		if sub == "save" {
			cmdVersionSave(c, parseOpts(rest))
		} else {
			cmdVersionRestore(c, parseOpts(rest))
		}
	case "publish":
		cmdPublish(c, parseOpts(args))
	case "channel":
		sub, rest := subcmd(args, "on|off")
		cmdChannel(c, sub, parseOpts(rest))
	case "preview":
		cmdPreview(c, args)
	case "coaching":
		sub, rest := subcmd(args, "list|get|update|publish|discard")
		o := parseOpts(rest)
		switch sub {
		case "list":
			cmdCoachingList(c, o)
		case "get":
			cmdCoachingGet(c, o)
		case "update":
			cmdCoachingUpdate(c, o)
		case "publish":
			cmdCoachingPublish(c, o)
		case "discard":
			cmdCoachingDiscard(c, o)
		}
	// legacy / extra
	case "policy":
		cmdPolicy(c, args)
	case "versions":
		need(args, "ft versions <agent>")
		cmdVersions(c, strings.Join(args, " "))
	case "stats":
		cmdStats(c, args)
	case "snapshot":
		cmdSnapshot(c)
	case "lint":
		cmdLint(c, strings.Join(args, " "))
	case "capture-help":
		cmdCaptureHelp()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ft — Forethought CLI (your own account)

Auth:
  login             Sign in (SRP); cache + auto-refresh tokens
  whoami            Show the signed-in user and org
  token             Print the current ID token

Read (free):
  agents list [-c CHAN] [--active]                 agents + channels they're live on
  agent get A [--channel email] [--published|--draft] [--out policy.md] [--raw]
                                                   intent, phrases, policy with {{chips}},
                                                   outline, response limit, tools, Journey
  agent versions A [--channel email]               versions + local restore points
  actions list [--json]                            workflow actions (secrets redacted)
  action get A [--out action.json]                 one action, secrets shown as [redacted]
  coaching list [--draft] [--agent A]              coaching (AI instructions)
  coaching get C [--out instructions.md]
  variables list / forms list / chips list         what you can insert with "/"
  search-sources get A [--channel email]           search tool sources + filters
  logs get CONV_ID [--full] / logs list [--agent A] [--since DATE] [--channel C]
  metrics [--agent A] [--channel C] [--from DATE] [--to DATE]

Write (draft only; prints a diff; --apply writes; backup in ~/ft_backup_YYYYMMDD/):
  agent set-policy A --channel email --file policy.md [--apply]
  agent set-setting A --channel email response_limit=3 [bypass_fallback_triggers=true] [--apply]
  search-sources set A --file fields.json [--tool ID] [--apply]
  version save A --channel email --name "…"
  version restore A --channel email (--version N | --name "…") [--apply]
  coaching update C [--file instructions.md] [--name N] [--active on|off] [--apply]
  coaching discard C [--apply]                     drop the draft, keep the published one
  action create --file action.json [--apply]       new secrets as ${env:NAME}; not used until added to an agent

Test:
  preview A|auto --channel email --thread "msg 1" "msg 2" … [--full]
                     runs the agent's REAL actions (FT has no preview mock);
                     asks first if any tool can change something

Live (need --confirm, one agent at a time, after approval):
  publish A --channel email --confirm
  channel on|off A --channel email [--draft] --confirm
  coaching publish C --confirm
  agent set-intent A [--name N] [--description TEXT|FILE] [--phrases FILE]
                   [--add-phrase "…"]… [--remove-phrase "…"]… --confirm
                                                   the Edit Agent dialog: no draft, routing changes at once
  action update A --file action.json --confirm    actions have no draft: live for every agent using it;
                                                   [redacted] values are kept; runs FT's impact check first

Other: policy, versions, stats, snapshot, lint, get PATH [--show-secrets], capture-help

Config: ~/.ft_cli_creds  (FT_API_BASE, FT_SUBDOMAIN, FT_EMAIL; FT_PASSWORD optional)
`)
}

// subcmd splits "<sub> rest…", failing with the allowed list if sub is missing.
func subcmd(args []string, allowed string) (string, []string) {
	if len(args) > 0 {
		for _, s := range strings.Split(allowed, "|") {
			if args[0] == s {
				return s, args[1:]
			}
		}
	}
	fail(fmt.Errorf("expected one of: %s", strings.ReplaceAll(allowed, "|", ", ")))
	return "", nil
}

func dropFirst(args []string, word string) []string {
	if len(args) > 0 && args[0] == word {
		return args[1:]
	}
	return args
}

func need(args []string, usage string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: %s", usage))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// readPassword prompts without echoing, using stty (macOS/Linux). It reads the
// whole line, so passwords with spaces work, and restores echo on Ctrl-C.
func readPassword(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	echoOn := func() {
		cmd := exec.Command("stty", "echo")
		cmd.Stdin = os.Stdin
		cmd.Run()
	}
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	off.Run()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		if _, ok := <-sig; ok {
			echoOn()
			fmt.Fprintln(os.Stderr)
			os.Exit(130)
		}
	}()
	defer func() {
		signal.Stop(sig)
		close(sig)
		echoOn()
		fmt.Fprintln(os.Stderr)
	}()
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func cmdLogin(c creds) {
	if c.Email == "" {
		fail(fmt.Errorf("set FT_EMAIL in ~/.ft_cli_creds"))
	}
	if c.Password == "" {
		// stdin fallback so the password never has to live in a file
		c.Password = readPassword(fmt.Sprintf("Password for %s: ", c.Email))
	}
	if c.Password == "" {
		fail(fmt.Errorf("no password entered"))
	}
	t, err := passwordLogin(c)
	if err != nil {
		fail(err)
	}
	if err := saveToken(t); err != nil {
		fail(err)
	}
	fmt.Printf("Logged in as %s. Token cached, valid ~%s.\n", t.Email, time.Until(t.ExpiresAt).Round(time.Minute))
}

// apiGet performs an authenticated read-only GET.
func apiGet(c creds, path string) ([]byte, error) {
	if dashboardAPI == "" {
		return nil, fmt.Errorf("set FT_API_BASE in ~/.ft_cli_creds")
	}
	tok, err := ensureToken(c)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, _ := http.NewRequest(http.MethodGet, dashboardAPI+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("HTTP %d: not authorized; run `ft login`", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %.400s", resp.StatusCode, b)
	}
	return b, nil
}

func cmdWhoami(c creds) {
	b, err := apiGet(c, "/ssel/users/current-user")
	if err != nil {
		fail(err)
	}
	var u map[string]any
	if json.Unmarshal(b, &u) != nil {
		fmt.Println(string(b))
		return
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := u[k]; ok && fmt.Sprint(v) != "" && v != nil {
				return fmt.Sprint(v)
			}
		}
		return ""
	}
	name := strings.TrimSpace(pick("first_name", "given_name") + " " + pick("last_name", "family_name"))
	fmt.Printf("Name:  %s\n", name)
	fmt.Printf("Email: %s\n", pick("email"))
	fmt.Printf("Role:  %s\n", pick("role", "role_name"))
	fmt.Printf("Org:   %s\n", pick("org_name", "organization_name", "org"))
	if c.Subdomain != "" {
		fmt.Printf("Sub:   %s\n", c.Subdomain)
	}
}

func cmdToken(c creds) {
	tok, err := ensureToken(c)
	if err != nil {
		fail(err)
	}
	fmt.Println(tok)
}

// cmdGet prints any GET response, with secrets redacted unless --show-secrets.
func cmdGet(c creds, path string, showSecrets bool) {
	b, err := apiGet(c, path)
	if err != nil {
		fail(err)
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		fmt.Println(string(b))
		return
	}
	n := 0
	if !showSecrets {
		v, n = redact(v)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
	if n > 0 {
		fmt.Fprintf(os.Stderr, "(%d secret value(s) redacted; --show-secrets to print them)\n", n)
	}
}
