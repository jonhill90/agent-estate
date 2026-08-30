// Command notify sends a message to Jon's Telegram bot.
//
// Reads AGENT_NOTIFY_TELEGRAM_TOKEN and AGENT_NOTIFY_TELEGRAM_CHAT_ID from
// $HOME/.local/state/agent-dotfiles-supervisor/notify.env (override with
// NOTIFY_ENV). Message body comes from a file argument or stdin.
//
// Credentials are read-only here: this never writes, resets or probes a
// credential store. A read failure is reported, not repaired.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func loadEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	env := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return env, s.Err()
}

func main() {
	envPath := os.Getenv("NOTIFY_ENV")
	if envPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "notify: cannot resolve home:", err)
			os.Exit(1)
		}
		envPath = filepath.Join(home, ".local", "state", "agent-dotfiles-supervisor", "notify.env")
	}
	env, err := loadEnv(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "notify: could not read %s: %v\n", envPath, err)
		os.Exit(1)
	}
	token, chat := env["AGENT_NOTIFY_TELEGRAM_TOKEN"], env["AGENT_NOTIFY_TELEGRAM_CHAT_ID"]
	if token == "" || chat == "" {
		fmt.Fprintln(os.Stderr, "notify: token or chat id absent -- refusing to send")
		os.Exit(1)
	}

	var body []byte
	if len(os.Args) > 1 {
		body, err = os.ReadFile(os.Args[1])
	} else {
		body, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "notify: cannot read message:", err)
		os.Exit(1)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		fmt.Fprintln(os.Stderr, "notify: empty message -- refusing to send")
		os.Exit(1)
	}

	// Telegram caps a single message at 4096 characters; split on rune
	// boundaries so a multi-byte character is never cut in half.
	const limit = 3900
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		n := limit
		if len(runes) < n {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}

	api := "https://api.telegram.org/bot" + token + "/sendMessage"
	for i, c := range chunks {
		resp, err := http.PostForm(api, url.Values{
			"chat_id":                  {chat},
			"text":                     {c},
			"disable_web_page_preview": {"true"},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "notify: send failed on part %d/%d: %v\n", i+1, len(chunks), err)
			os.Exit(1)
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "notify: telegram rejected part %d/%d: %s %s\n", i+1, len(chunks), resp.Status, string(out))
			os.Exit(1)
		}
	}
	fmt.Printf("notify: sent %d part(s)\n", len(chunks))
}
