package api

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/knowledge"
	"github.com/israel-duff/taskiem/engine/knowledge/extract"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A document sent in chat for the assistant to know (decision 0062 V10,
// K7): a PDF or Word document an owner sends on WhatsApp or Telegram
// becomes a source of what the business's assistant knows, as one
// uploaded on the What your assistant knows page, waiting there for Use
// this. A spreadsheet sent in chat stays a table import.

// knowledgeChatMax is the largest document read from chat.
const knowledgeChatMax = 10 << 20

// isKnowledgeDocument: a PDF or a Word (.docx) document.
func isKnowledgeDocument(doc *whatsapp.Media) bool {
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(doc.MimeType, ";", 2)[0]))
	switch mime {
	case "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return true
	}
	switch strings.ToLower(path.Ext(doc.Name)) {
	case ".pdf", ".docx":
		return true
	}
	return false
}

// waKnowledgeDocument reads a document sent in chat into a new source.
func (s *Server) waKnowledgeDocument(ctx context.Context, c *chat) error {
	doc := c.in.Document
	if !c.p.Can(PermSecretManage) {
		return c.say(ctx, s, c.t("wa.knowledge.denied", "org", c.tenant.Name))
	}
	if c.ch.Name() == "slack" {
		return c.say(ctx, s, c.t("wa.knowledge.slack", "link", s.waLink(c, "/knowledge")))
	}
	if s.Knowledge == nil {
		return c.say(ctx, s, c.t("wa.knowledge.unavailable"))
	}
	if !s.limiter("wa-file:"+c.addr, time.Minute, 5).Allow() {
		return c.say(ctx, s, c.t("wa.file.too_many"))
	}
	data, _, _, err := s.fetchMedia(ctx, c, doc, knowledgeChatMax)
	if errors.Is(err, whatsapp.ErrMediaTooLarge) || err == nil && len(data) > knowledgeChatMax {
		return c.say(ctx, s, c.t("wa.knowledge.too_big", "link", s.waLink(c, "/knowledge")))
	}
	if err != nil {
		s.Logger.Warn("chat: fetching a knowledge document", "err", err)
		return c.say(ctx, s, c.t("wa.file.not_fetched"))
	}
	select {
	case knowledgeExtracting <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	text, err := extract.FileText(data)
	<-knowledgeExtracting
	switch {
	case errors.Is(err, extract.ErrScanned):
		return c.say(ctx, s, c.t("wa.knowledge.scanned"))
	case errors.Is(err, extract.ErrTooLarge):
		return c.say(ctx, s, c.t("wa.knowledge.too_big", "link", s.waLink(c, "/knowledge")))
	case err != nil:
		return c.say(ctx, s, c.t("wa.knowledge.unreadable"))
	}
	name := strings.TrimSpace(doc.Name)
	if name == "" {
		name = "Document from chat"
	}
	if utf8.RuneCountInString(name) > 120 {
		name = string([]rune(name)[:120])
	}
	cfg, _ := json.Marshal(map[string]any{"name": name, "text": text})
	src, err := s.Knowledge.Create(ctx, c.tenant.ID, knowledge.File, name, cfg, "user:"+c.p.UserID.String(), func(tx pgx.Tx, id uuid.UUID) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "knowledge.source.create", id.String(), map[string]any{"kind": knowledge.File, "title": name})
	})
	if err != nil {
		s.Logger.Warn("chat: adding a knowledge document", "err", err)
		return c.say(ctx, s, c.t("wa.knowledge.unreadable"))
	}
	// Read now, so the page shows what it says; it waits there for Use this.
	rctx, cancel := context.WithTimeout(context.WithValue(ctx, knowledgeActorKey{}, "user:"+c.p.UserID.String()), knowledgeReadBudget)
	defer cancel()
	if _, err := s.Knowledge.Refresh(rctx, c.tenant.ID, src.ID); err != nil && !errors.Is(err, knowledge.ErrBusy) {
		s.Logger.Info("chat: reading a knowledge document", "err", err)
	}
	return c.say(ctx, s, c.t("wa.knowledge.added", "name", name, "link", s.waLink(c, "/knowledge")))
}
