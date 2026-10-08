package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// TestEdgeWhatsAppBuild builds over WhatsApp through the edge role's
// server: the message reaches the configured model, as on the api role.
func TestEdgeWhatsAppBuild(t *testing.T) {
	const pnid, token, secret = "106540352242922", "EAAplatform", "app-secret-platform"
	env := rt.New(t)
	graph := whatsapptest.New(t, pnid, token)
	log := slog.New(slog.DiscardHandler)
	wa := whatsapp.New(env.Store.Pool, whatsapp.Config{PhoneNumberID: pnid, AccessToken: token, AppSecret: secret, VerifyToken: "vt",
		TokenKey: bytes.Repeat([]byte{42}, 32), GraphURL: graph.URL, DisplayNumber: "+15550001111"}, env.Egress, log)
	wa.Secrets = env.Vault
	e := &engine{log: log, pool: env.Store.Pool, store: env.Store, vault: env.Vault, registry: env.Registry, wa: wa}

	// The edge role's server, from the configuration serve reads.
	edge, err := edgeServer(e, config{AI: ai.Config{Provider: "fake", Effort: "high", MaxTokens: ai.DefaultMaxTokens}}, log)
	if err != nil {
		t.Fatal(err)
	}
	if edge.AI == nil {
		t.Fatal("the edge role has no model")
	}
	model, ok := edge.AI.Provider.(*ai.Fake)
	if !ok {
		t.Fatalf("model: %T", edge.AI.Provider)
	}
	edgeTS := httptest.NewServer(http.StripPrefix("/channels/whatsapp", edge.WhatsAppHooks()))
	t.Cleanup(edgeTS.Close)

	// The web app (the api role) signs a tenant up and binds a number.
	web := &api.Server{Store: env.Store, Vault: env.Vault, Registry: env.Registry, AllowSignup: true, SignupPerAddress: -1,
		Egress: env.Egress, Logger: log, WhatsApp: wa, PublicURL: "https://taskiem.test"}
	webTS := httptest.NewServer(web.Handler())
	t.Cleanup(webTS.Close)
	call := func(want int, method, path, tok string, body any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, webTS.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	const pw, number = "correct horse battery", "+2348010000077"
	call(201, "POST", "/v1/signup", "", map[string]any{"tenant": "Edge", "email": "owner@edge.test", "name": "Owner", "password": pw})
	if _, err := env.DB.Admin.Exec(context.Background(), `UPDATE users SET email_verified_at = now() WHERE email = 'owner@edge.test'`); err != nil {
		t.Fatal(err)
	}
	tok := call(200, "POST", "/v1/auth/login", "", map[string]any{"email": "owner@edge.test", "password": pw, "bearer": true})["token"].(string)
	call(202, "POST", "/v1/me/whatsapp", tok, map[string]any{"number": number, "password": pw})
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(graph.Last(t, number).Text)
	call(200, "POST", "/v1/me/whatsapp/verify", tok, map[string]any{"code": code})

	before := len(graph.Sent(number))
	body := whatsapptest.Delivery(pnid, number, "wamid.edge1", whatsapptest.Text("build a workflow that texts me good morning every day"))
	req, _ := http.NewRequest("POST", edgeTS.URL+"/channels/whatsapp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", whatsapp.Sign(secret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delivery: %d", resp.StatusCode)
	}
	// The draft (or its failure) follows "Working on it" once the model answers.
	for end := time.Now().Add(20 * time.Second); len(graph.Sent(number)) < before+2 && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
	var replies []string
	for _, s := range graph.Sent(number)[before:] {
		replies = append(replies, s.Text)
	}
	all := strings.Join(replies, "\n")
	if strings.Contains(all, "not set up") || !strings.Contains(all, "Working on it") {
		t.Errorf("replies: %q", replies)
	}
	if len(model.Requests()) == 0 {
		t.Error("the build did not reach the model")
	}
}
