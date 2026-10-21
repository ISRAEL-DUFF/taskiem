package inmail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"golang.org/x/net/netutil"
)

// Store is what the mail role may do in the database, through the narrow
// functions taskiem_public is granted (migration 00192).
type Store interface {
	// Recipient is the live endpoint for an address's public id.
	Recipient(ctx context.Context, publicID string) (uuid.UUID, bool, error)
	// Receive leaves a sealed message for an endpoint; false is "not now"
	// (rate limited, or the address stopped taking mail).
	Receive(ctx context.Context, id, endpoint uuid.UUID, idem, meta, raw []byte) (bool, error)
}

// Sealer seals what the mail role receives for the api role (forms.Keys).
type Sealer interface {
	SealBytes(what string, raw []byte) ([]byte, error)
}

// MetaAAD and RawAAD bind a sealed inbox row's parts to it.
func MetaAAD(id uuid.UUID) string { return "email meta " + id.String() }
func RawAAD(id uuid.UUID) string  { return "email raw " + id.String() }

// Server is the mail role: an SMTP server that takes mail for the
// addresses of live email triggers and nothing else.
type Server struct {
	// Domain is the mail domain (TASKIEM_MAIL_DOMAIN): addresses are
	// <public id>@Domain.
	Domain string
	// Hostname is the name the server greets with (default mx.Domain).
	Hostname string
	Store    Store
	Seal     Sealer
	// TLS is offered with STARTTLS; nil makes a self-signed certificate,
	// which is enough for mail servers, which do not check it.
	TLS      *tls.Config
	Resolver Resolver
	// MaxConnections caps connections at once (default 200).
	MaxConnections int
	Logger         *slog.Logger
	Now            func() time.Time
}

var localPart = regexp.MustCompile(`^[a-z0-9]{12}$`)

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Serve takes connections on l until ctx ends.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	srv := smtp.NewServer(smtp.BackendFunc(func(c *smtp.Conn) (smtp.Session, error) {
		return &session{s: s, conn: c}, nil
	}))
	srv.Domain = s.Hostname
	if srv.Domain == "" {
		srv.Domain = "mx." + s.Domain
	}
	srv.MaxMessageBytes = MaxMessageBytes
	srv.MaxRecipients = 10
	srv.ReadTimeout = 2 * time.Minute
	srv.WriteTimeout = 2 * time.Minute
	srv.EnableSMTPUTF8 = true
	srv.ErrorLog = errorLog{s.log()}
	srv.TLSConfig = s.TLS
	if srv.TLSConfig == nil {
		cfg, err := SelfSigned(srv.Domain)
		if err != nil {
			return err
		}
		srv.TLSConfig = cfg
	}
	limit := s.MaxConnections
	if limit <= 0 {
		limit = 200
	}
	l = netutil.LimitListener(l, limit)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// ctx has ended: shutting down gets a deadline of its own.
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		case <-done:
		}
	}()
	defer close(done)
	err := srv.Serve(l)
	if errors.Is(err, smtp.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return err
}

type errorLog struct{ l *slog.Logger }

func (e errorLog) Printf(format string, v ...any) { e.l.Debug("smtp: " + fmt.Sprintf(format, v...)) }
func (e errorLog) Println(v ...any)               { e.l.Debug("smtp: " + fmt.Sprint(v...)) }

type rcpt struct {
	address  string
	endpoint uuid.UUID
}

type session struct {
	s     *Server
	conn  *smtp.Conn
	from  string
	rcpts []rcpt
}

var (
	errNoSuchAddress = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "No such address here"}
	errNotOurs       = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "This server takes mail for its own addresses only"}
	errTryLater      = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Not now: try again later"}
	errMalformed     = &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 6, 0}, Message: "The message cannot be read"}
)

func (ss *session) Mail(from string, _ *smtp.MailOptions) error {
	ss.from = from
	return nil
}

func (ss *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	to = strings.ToLower(strings.Trim(strings.TrimSpace(to), "<>"))
	at := strings.LastIndexByte(to, '@')
	if at < 0 || to[at+1:] != strings.ToLower(ss.s.Domain) {
		return errNotOurs
	}
	local := to[:at]
	if i := strings.IndexByte(local, '+'); i >= 0 { // name+tag@ reaches name@
		local = local[:i]
	}
	if !localPart.MatchString(local) {
		return errNoSuchAddress
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ep, ok, err := ss.s.Store.Recipient(ctx, local)
	if err != nil {
		ss.s.log().Error("mail: recipient lookup failed", "err", err)
		return errTryLater
	}
	if !ok {
		return errNoSuchAddress
	}
	for _, r := range ss.rcpts {
		if r.endpoint == ep {
			return nil
		}
	}
	ss.rcpts = append(ss.rcpts, rcpt{address: local + "@" + ss.s.Domain, endpoint: ep})
	return nil
}

func (ss *session) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err // too large: go-smtp answers 552
	}
	hdr, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return errMalformed
	}
	from := ""
	if a, err := mail.ParseAddress(hdr.Header.Get("From")); err == nil {
		from = a.Address
	}
	var ip net.IP
	if a, ok := ss.conn.Conn().RemoteAddr().(*net.TCPAddr); ok {
		ip = a.IP
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth := Authenticate(ctx, ss.s.Resolver, raw, from, ss.from, ss.conn.Hostname(), ip)
	messageID := strings.Trim(strings.TrimSpace(hdr.Header.Get("Message-Id")), "<>")
	for _, rc := range ss.rcpts {
		id := uuid.Must(uuid.NewV7())
		h := sha256.New()
		h.Write(rc.endpoint[:])
		if messageID != "" {
			h.Write([]byte("message-id\x00" + messageID))
		} else {
			h.Write(raw)
		}
		meta := Meta{Reference: id.String(), Address: rc.address, MailFrom: ss.from, Helo: ss.conn.Hostname(), ReceivedAt: ss.s.now().UTC(), Auth: auth}
		if ip != nil {
			meta.RemoteIP = ip.String()
		}
		sealedMeta, err := sealJSON(ss.s.Seal, MetaAAD(id), meta)
		if err != nil {
			return errTryLater
		}
		sealedRaw, err := ss.s.Seal.SealBytes(RawAAD(id), raw)
		if err != nil {
			return errTryLater
		}
		ok, err := ss.s.Store.Receive(ctx, id, rc.endpoint, h.Sum(nil), sealedMeta, sealedRaw)
		if err != nil {
			ss.s.log().Error("mail: storing a message failed", "err", err)
			return errTryLater
		}
		if !ok {
			return errTryLater
		}
	}
	ss.s.log().Info("mail: received", "recipients", len(ss.rcpts), "size", len(raw), "spf", auth.SPF, "aligned", auth.Aligned)
	return nil
}

func (ss *session) Reset() {
	ss.from = ""
	ss.rcpts = nil
}

func (ss *session) Logout() error { return nil }

// SelfSigned is a certificate for STARTTLS when the operator gives none.
func SelfSigned(host string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(2, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, nil
}
