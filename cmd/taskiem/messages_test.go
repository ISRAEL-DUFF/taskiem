package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestMessagesConfig(t *testing.T) {
	for _, k := range []string{"TASKIEM_SMS_TERMII_API_KEY", "TASKIEM_SMS_TERMII_SENDER_ID", "TASKIEM_SMS_AT_USERNAME", "TASKIEM_SMS_AT_API_KEY",
		"TASKIEM_SMS_TERMII_CHANNEL", "TASKIEM_SMS_RATE", "TASKIEM_SMS_COUNTRIES", "TASKIEM_MESSAGE_HASH_KEY", "TASKIEM_MESSAGE_FROM", "TASKIEM_ALERT_FROM"} {
		t.Setenv(k, "")
	}
	c, err := messagesConfigFromEnv()
	if err != nil || len(c.Countries) != 1 || c.Countries[0] != "234" || c.Rate != 10 || len(c.smsProviders(slog.New(slog.NewTextHandler(io.Discard, nil)))) != 0 {
		t.Fatalf("defaults: %+v %v", c, err)
	}

	t.Setenv("TASKIEM_SMS_TERMII_API_KEY", "tk")
	if _, err := messagesConfigFromEnv(); err == nil {
		t.Fatal("a Termii key without a sender ID")
	}
	t.Setenv("TASKIEM_SMS_TERMII_SENDER_ID", "Taskiem")
	t.Setenv("TASKIEM_SMS_AT_USERNAME", "taskiem")
	t.Setenv("TASKIEM_SMS_AT_API_KEY", "atk")
	t.Setenv("TASKIEM_SMS_COUNTRIES", "+234, 233")
	t.Setenv("TASKIEM_ALERT_FROM", "alerts@taskiem.test")
	c, err = messagesConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if p := c.smsProviders(slog.New(slog.NewTextHandler(io.Discard, nil))); len(p) != 2 || p[0].Name() != "termii" || p[1].Name() != "africastalking" {
		t.Fatalf("providers %v", p)
	}
	if len(c.Countries) != 2 || c.Countries[1] != "233" || c.From != "alerts@taskiem.test" {
		t.Fatalf("%+v", c)
	}

	for k, v := range map[string]string{
		"TASKIEM_SMS_TERMII_CHANNEL": "whatsapp",
		"TASKIEM_SMS_RATE":           "-1",
		"TASKIEM_SMS_COUNTRIES":      "nigeria",
		"TASKIEM_MESSAGE_HASH_KEY":   "c2hvcnQ=",
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := messagesConfigFromEnv(); err == nil {
				t.Errorf("%s=%q accepted", k, v)
			}
		})
	}
}
