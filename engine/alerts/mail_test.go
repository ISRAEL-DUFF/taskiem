package alerts

import (
	"strings"
	"testing"
	"time"
)

func TestBuildEmailKeepsTitlesOutOfHeaders(t *testing.T) {
	msg := string(BuildEmail("alerts@taskiem.test", []string{"ops@acme.test"}, "loan failed\r\nBcc: thief@evil.test", "line one\nline two", "id-1"))
	head, body, _ := strings.Cut(msg, "\r\n\r\n")
	if strings.Contains(head, "\r\nBcc:") {
		t.Errorf("a title added a header:\n%s", head)
	}
	if body != "line one\r\nline two\r\n" {
		t.Errorf("body %q", body)
	}
}

func TestBackoffAndThresholds(t *testing.T) {
	if backoff(1) != time.Minute || backoff(3) != 16*time.Minute || backoff(8) != 6*time.Hour {
		t.Errorf("backoff %v %v %v", backoff(1), backoff(3), backoff(8))
	}
	if d, _ := (RuleConfig{}).ThresholdFor(SlowRun); d != time.Hour {
		t.Errorf("default slow-run threshold %v", d)
	}
	if _, err := (RuleConfig{Threshold: "-5m"}).ThresholdFor(SlowRun); err == nil {
		t.Error("a negative threshold was accepted")
	}
	if slackEscape("<!channel> & co") != "&lt;!channel&gt; &amp; co" {
		t.Error("slack escaping")
	}
}
