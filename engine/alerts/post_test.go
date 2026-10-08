package alerts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection reset by peer")
}

// A Slack webhook URL is itself a secret: a failed delivery's error, which
// is stored and shown, names only the host.
func TestDeliveryErrorsNameOnlyTheHost(t *testing.T) {
	a := &Alerter{Client: &http.Client{Transport: failing{}}}
	hook := "https://hooks.slack.com/services/T0SECRET/B0SECRET/xoxsecretpath"
	err := a.post(context.Background(), uuid.New(), hook, []byte(`{}`), nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "secretpath") ||
		!strings.Contains(err.Error(), "hooks.slack.com") || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("delivery error: %v", err)
	}
	if err := a.post(context.Background(), uuid.New(), "https://hooks.slack.com/services/%zzSECRET", []byte(`{}`), nil); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("bad destination: %v", err)
	}
}
