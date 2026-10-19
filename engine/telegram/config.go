package telegram

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Config is the operator's bot (docs/telegram.md). The credentials come
// from the environment (the deployment's Secret), never from chart values
// or the database.
type Config struct {
	BotToken      string // TASKIEM_TELEGRAM_BOT_TOKEN
	WebhookSecret string // TASKIEM_TELEGRAM_WEBHOOK_SECRET: 1-256 chars [A-Za-z0-9_-]; checked against X-Telegram-Bot-Api-Secret-Token
	BotUsername   string // TASKIEM_TELEGRAM_BOT_USERNAME, without @: [A-Za-z0-9_]{5,32}, for t.me links
	APIURL        string // TASKIEM_TELEGRAM_API_URL: DefaultAPIURL unless overridden (tests)
}

var (
	tokenRe    = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$`)
	secretRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)
)

// ConfigFromEnv reads the bot's settings; nil when
// TASKIEM_TELEGRAM_BOT_TOKEN is unset (the channel is off). Errors name the
// variable, never its value.
func ConfigFromEnv(lookup func(string) (string, bool)) (*Config, error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	c := &Config{
		BotToken: get("TASKIEM_TELEGRAM_BOT_TOKEN"), WebhookSecret: get("TASKIEM_TELEGRAM_WEBHOOK_SECRET"),
		BotUsername: strings.TrimPrefix(get("TASKIEM_TELEGRAM_BOT_USERNAME"), "@"), APIURL: get("TASKIEM_TELEGRAM_API_URL"),
	}
	if c.BotToken == "" {
		return nil, nil
	}
	var missing []string
	for _, kv := range [][2]string{{"TASKIEM_TELEGRAM_WEBHOOK_SECRET", c.WebhookSecret}, {"TASKIEM_TELEGRAM_BOT_USERNAME", c.BotUsername}} {
		if kv[1] == "" {
			missing = append(missing, kv[0])
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("TASKIEM_TELEGRAM_BOT_TOKEN is set but %s is not", strings.Join(missing, ", "))
	}
	if !tokenRe.MatchString(c.BotToken) {
		return nil, fmt.Errorf("TASKIEM_TELEGRAM_BOT_TOKEN is not a bot token (<bot id>:<secret>, as @BotFather gives it)")
	}
	if !secretRe.MatchString(c.WebhookSecret) {
		return nil, fmt.Errorf("TASKIEM_TELEGRAM_WEBHOOK_SECRET must be 1-256 characters A-Z, a-z, 0-9, _ or - (openssl rand -hex 32)")
	}
	if !usernameRe.MatchString(c.BotUsername) {
		return nil, fmt.Errorf("TASKIEM_TELEGRAM_BOT_USERNAME must be the bot's username without @: 5-32 characters A-Z, a-z, 0-9 or _")
	}
	if c.APIURL == "" {
		c.APIURL = DefaultAPIURL
	}
	if u, err := url.Parse(c.APIURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("TASKIEM_TELEGRAM_API_URL must be an http(s) URL")
	}
	c.APIURL = strings.TrimRight(c.APIURL, "/")
	return c, nil
}
