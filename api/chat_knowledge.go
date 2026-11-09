package api

import (
	"archive/zip"
	"bytes"
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

// isOldWord: a Word document of before 2007 (.doc), which is not read.
func isOldWord(doc *whatsapp.Media) bool {
	return strings.EqualFold(path.Ext(doc.Name), ".doc") || strings.HasPrefix(strings.ToLower(doc.MimeType), "application/msword")
}

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

// documentContent: what the bytes are, whatever the name says: "pdf",
// "docx", or "".
func documentContent(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
			for _, f := range zr.File {
				if f.Name == "word/document.xml" {
					return "docx"
				}
			}
		}
	}
	return ""
}

// waKnowledgeDocument reads a document sent in chat into a source: a new
// one, or the one of the same name sent before (its new version). The
// reading is done after the chat's answer, with its own time (a webhook
// gives a chat seconds), and the person is told how it went.
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
	if documentContent(data) == "" {
		// Named a PDF or Word document, but it is not one.
		return c.say(ctx, s, c.t("wa.knowledge.unreadable"))
	}
	name := strings.TrimSpace(doc.Name)
	if name == "" {
		name = "Document"
	}
	if utf8.RuneCountInString(name) > 120 {
		name = string([]rune(name)[:120])
	}
	if err := c.say(ctx, s, c.t("wa.knowledge.reading", "name", name)); err != nil {
		return err
	}
	tenant, actor, link := c.tenant.ID, c.p.Actor(), s.waLink(c, "/knowledge")
	s.background(ctx, "knowledge document from chat", func(ctx context.Context) {
		msg := s.knowledgeFromChat(ctx, c, tenant, actor, name, data, link)
		if err := c.say(ctx, s, msg); err != nil {
			s.Logger.Warn("chat: telling how a knowledge document was read", "err", err)
		}
	})
	return nil
}

// knowledgeFromChat extracts, keeps and reads a document sent in chat; it
// answers what to tell the person.
func (s *Server) knowledgeFromChat(ctx context.Context, c *chat, tenant uuid.UUID, actor, name string, data []byte, link string) string {
	select {
	case knowledgeExtracting <- struct{}{}:
	case <-ctx.Done():
		return c.t("wa.knowledge.unreadable")
	}
	text, err := extract.FileText(data)
	<-knowledgeExtracting
	switch {
	case errors.Is(err, extract.ErrScanned):
		return c.t("wa.knowledge.scanned")
	case errors.Is(err, extract.ErrTooLarge):
		return c.t("wa.knowledge.too_big", "link", link)
	case err != nil:
		return c.t("wa.knowledge.unreadable")
	}
	cfg, _ := json.Marshal(map[string]any{"name": name, "text": text})
	audit := func(action string) knowledge.Hook {
		return func(tx pgx.Tx, id uuid.UUID) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, action, id.String(), map[string]any{"kind": knowledge.File, "title": name, "document": name})
		}
	}
	// The same document sent again (a new menu): its new version.
	var id uuid.UUID
	updated := false
	if list, err := s.Knowledge.List(ctx, tenant); err == nil {
		for _, src := range list {
			if src.Kind == knowledge.File && src.Title == name {
				if err := s.Knowledge.Update(ctx, tenant, src.ID, nil, cfg, audit("knowledge.source.update")); err != nil {
					return s.knowledgeChatRefused(c, err, link)
				}
				id, updated = src.ID, true
				break
			}
		}
	}
	if !updated {
		src, err := s.Knowledge.Create(ctx, tenant, knowledge.File, name, cfg, actor, audit("knowledge.source.create"))
		if err != nil {
			return s.knowledgeChatRefused(c, err, link)
		}
		id = src.ID
	}
	rctx, cancel := context.WithTimeout(context.WithValue(ctx, knowledgeActorKey{}, actor), knowledgeReadBudget)
	defer cancel()
	if _, err := s.Knowledge.Refresh(rctx, tenant, id); err != nil {
		s.Logger.Info("chat: reading a knowledge document", "err", err)
		return c.t("wa.knowledge.still_reading", "name", name, "link", link)
	}
	if updated {
		return c.t("wa.knowledge.updated", "name", name, "link", link)
	}
	return c.t("wa.knowledge.added", "name", name, "link", link)
}

// knowledgeChatRefused says why a document could not be kept.
func (s *Server) knowledgeChatRefused(c *chat, err error, link string) string {
	if errors.Is(err, knowledge.ErrBadInput) {
		return c.t("wa.knowledge.not_kept", "why", strings.TrimPrefix(err.Error(), "knowledge: bad input: "), "link", link)
	}
	s.Logger.Warn("chat: keeping a knowledge document", "err", err)
	return c.t("wa.knowledge.unreadable")
}
