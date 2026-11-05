package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mini Apps (docs/spec/chat-way-in.md 7.2): a page opened from a web_app
// button gets Telegram's initData, signed for the bot. VerifyInitData
// checks it as Telegram's documentation says: the data-check string (each
// field but hash, as key=value, sorted, joined by newlines) under
// HMAC-SHA-256 with the key HMAC-SHA-256("WebAppData", bot token) must be
// the hash; and auth_date must be within maxAge. It returns the Telegram
// account the page was opened by.

// ErrInitData: initData that is missing, not signed for this bot, or old.
var ErrInitData = errors.New("telegram: the Mini App's data is not valid")

func VerifyInitData(botToken, initData string, maxAge time.Duration, now time.Time) (int64, error) {
	if botToken == "" || initData == "" || len(initData) > 8192 {
		return 0, ErrInitData
	}
	vals, err := url.ParseQuery(initData)
	if err != nil {
		return 0, ErrInitData
	}
	hash := vals.Get("hash")
	if len(hash) != 64 {
		return 0, ErrInitData
	}
	var lines []string
	for k, v := range vals {
		if k == "hash" || len(v) != 1 {
			continue
		}
		lines = append(lines, k+"="+v[0])
	}
	sort.Strings(lines)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	want, err := hex.DecodeString(hash)
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return 0, ErrInitData
	}
	at, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil {
		return 0, ErrInitData
	}
	if age := now.Sub(time.Unix(at, 0)); age > maxAge || age < -time.Minute {
		return 0, ErrInitData
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil || user.ID <= 0 {
		return 0, ErrInitData
	}
	return user.ID, nil
}

// SignInitData signs initData for a bot as Telegram does: for tests and
// the fake.
func SignInitData(botToken string, user int64, at time.Time) string {
	vals := url.Values{}
	vals.Set("auth_date", strconv.FormatInt(at.Unix(), 10))
	vals.Set("query_id", "AAHdF6IQAAAAAN0XohDhrOrc")
	vals.Set("user", `{"id":`+strconv.FormatInt(user, 10)+`,"first_name":"Ada"}`)
	var lines []string
	for k, v := range vals {
		lines = append(lines, k+"="+v[0])
	}
	sort.Strings(lines)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	vals.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return vals.Encode()
}
