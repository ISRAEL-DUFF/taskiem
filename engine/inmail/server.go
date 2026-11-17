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
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"golang.org/x/net/netutil"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"

	"github.com/israel-duff/taskiem/engine/telemetry"
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
	// MaxConnectionsPerAddress caps one remote address's connections at
	// once (default 10): one host cannot hold them all. Over it, a
	// connection is answered 421 and closed.
	MaxConnectionsPerAddress int
	// MessagesPerMinute is how many messages one remote address may start
	// a minute (default 60, with as many at once); more are answered 451.
	MessagesPerMinute int
	// MaxTransfers caps the messages being received at once (default 8):
	// each is held in memory, up to MaxMessageBytes, while it is checked
	// and sealed. More are answered 451, to be sent again.
	MaxTransfers int
	// IdleTimeout is how long a connection may say nothing before it has
	// started a message (default 30 seconds), and four times it is the
	// longest it may take to start one. After MAIL FROM a command may
	// take two minutes, as mail servers expect.
	IdleTimeout time.Duration
	// DataTimeout is the longest one message may take to arrive (default
	// 5 minutes).
	DataTimeout time.Duration
	Logger      *slog.Logger
	Now         func() time.Time

	// Set by Serve.
	transfers chan struct{}
	starts    *formguard.Limiter
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
	perAddr := s.MaxConnectionsPerAddress
	if perAddr <= 0 {
		perAddr = 10
	}
	idle := s.IdleTimeout
	if idle <= 0 {
		idle = 30 * time.Second
	}
	transfers := s.MaxTransfers
	if transfers <= 0 {
		transfers = 8
	}
	perMinute := s.MessagesPerMinute
	if perMinute <= 0 {
		perMinute = 60
	}
	s.transfers = make(chan struct{}, transfers)
	s.starts = formguard.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), perMinute, 20000, 10*time.Minute)
	// The ceiling for everyone, then each address's share of it: a
	// connection over its address's share is answered and closed at once,
	// giving its place back.
	l = &addrListener{Listener: netutil.LimitListener(l, limit), max: perAddr, idle: idle, n: map[string]int{}}
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

// addrListener caps each remote address's connections at once, and gives
// every connection the idle limits of guardConn.
type addrListener struct {
	net.Listener
	max  int
	idle time.Duration
	mu   sync.Mutex
	n    map[string]int
}

func remoteIP(c net.Conn) string {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return c.RemoteAddr().String()
}

func (l *addrListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ip := remoteIP(c)
		l.mu.Lock()
		over := l.n[ip] >= l.max
		if !over {
			l.n[ip]++
		}
		l.mu.Unlock()
		if !over {
			return &guardConn{Conn: c, l: l, ip: ip, idle: l.idle, opened: time.Now()}, nil
		}
		telemetry.Ingest.WithLabelValues("email_smtp", "too_many_connections").Inc()
		// Answered away from the accept loop: a peer that does not read
		// cannot hold it up.
		go func() {
			_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.WriteString(c, "421 4.7.0 Too many connections from your address: try again later\r\n")
			_ = c.Close()
		}()
	}
}

// guardConn is a connection counted against its address. Until it starts
// a message (MAIL FROM) each read waits at most idle, and it has four
// times idle in all: a connection that only sits there, or only says
// NOOP, is ended. After that go-smtp's own timeouts apply.
type guardConn struct {
	net.Conn
	l       *addrListener
	ip      string
	idle    time.Duration
	opened  time.Time
	started atomic.Bool
	closed  sync.Once
}

func (c *guardConn) SetReadDeadline(t time.Time) error {
	if !c.started.Load() {
		lim := time.Now().Add(c.idle)
		if end := c.opened.Add(4 * c.idle); end.Before(lim) {
			lim = end
		}
		if t.IsZero() || t.After(lim) {
			t = lim
		}
	}
	return c.Conn.SetReadDeadline(t)
}

func (c *guardConn) Close() error {
	c.closed.Do(func() {
		c.l.mu.Lock()
		if c.l.n[c.ip]--; c.l.n[c.ip] <= 0 {
			delete(c.l.n, c.ip)
		}
		c.l.mu.Unlock()
	})
	return c.Conn.Close()
}

// guardOf is a session's guardConn, under TLS or not; nil when the
// connection did not come through Serve's listener.
func guardOf(c net.Conn) *guardConn {
	if t, ok := c.(*tls.Conn); ok {
		c = t.NetConn()
	}
	g, _ := c.(*guardConn)
	return g
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
	bad   int // unknown recipients named on this connection
}

// maxBadRcpts unknown recipients end a connection. Each costs a lookup
// on the mail role's small pool, and go-smtp's recipient cap counts only
// those accepted, so without it one connection could look up addresses
// without end (F66). Senders retry on the 421 later.
const maxBadRcpts = 5

var (
	errNoSuchAddress = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "No such address here"}
	errNotOurs       = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "This server takes mail for its own addresses only"}
	errTryLater      = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Not now: try again later"}
	errMalformed     = &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 6, 0}, Message: "The message cannot be read"}
	errTooManyBad    = &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Too many unknown recipients: closing"}
	errTooMany       = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 1}, Message: "Too many messages from your address: try again later"}
	errBusy          = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 2}, Message: "Busy: try again later"}
	errTooSlow       = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 4, 2}, Message: "The message took too long to arrive: try again"}
)

func (ss *session) Mail(from string, _ *smtp.MailOptions) error {
	// How many messages an address may start (Serve sets the limit).
	if g := guardOf(ss.conn.Conn()); g != nil {
		if ss.s.starts != nil && !ss.s.starts.Allow(g.ip) {
			telemetry.Ingest.WithLabelValues("email_smtp", "rate_limited").Inc()
			return errTooMany
		}
		g.started.Store(true)
	}
	ss.from = from
	return nil
}

// slowReader fails a read begun after its deadline: a message trickled in
// does not hold a transfer's place for ever.
type slowReader struct {
	r   io.Reader
	end time.Time
}

func (s *slowReader) Read(p []byte) (int, error) {
	if time.Now().After(s.end) {
		return 0, errTooSlow
	}
	return s.r.Read(p)
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
	if ss.bad >= maxBadRcpts {
		ss.hangUp()
		return errTooManyBad
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ep, ok, err := ss.s.Store.Recipient(ctx, local)
	if err != nil {
		ss.s.log().Error("mail: recipient lookup failed", "err", err)
		return errTryLater
	}
	if !ok {
		telemetry.Ingest.WithLabelValues("email_smtp", "unknown_recipient").Inc()
		ss.bad++
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

// hangUp answers 421 and closes the connection. go-smtp has no way for
// a session to end its connection after an answer: the reply is written
// here, and go-smtp's own copy of it, written after, goes nowhere.
func (ss *session) hangUp() {
	c := ss.conn.Conn()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = fmt.Fprintf(c, "%d %d.%d.%d %s\r\n", errTooManyBad.Code, errTooManyBad.EnhancedCode[0], errTooManyBad.EnhancedCode[1],
		errTooManyBad.EnhancedCode[2], errTooManyBad.Message)
	_ = c.Close()
}

func (ss *session) Data(r io.Reader) error {
	// Only so many messages are received at once: each is held whole in
	// memory while it is checked and sealed. The rest are told to send
	// again; what they send meanwhile is read and dropped by go-smtp,
	// never kept.
	if ss.s.transfers != nil {
		select {
		case ss.s.transfers <- struct{}{}:
			defer func() { <-ss.s.transfers }()
		default:
			telemetry.Ingest.WithLabelValues("email_smtp", "busy").Inc()
			return errBusy
		}
	}
	limit := ss.s.DataTimeout
	if limit <= 0 {
		limit = 5 * time.Minute
	}
	raw, err := io.ReadAll(&slowReader{r: r, end: time.Now().Add(limit)})
	if err != nil {
		return err // too large: go-smtp answers 552
	}
	hdr, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return errMalformed
	}
	// One From, one Message-ID: a second would be read by people and
	// skipped by the signature check (headers.go). Nothing is stored.
	if name := RepeatedHeader(raw); name != "" {
		telemetry.Ingest.WithLabelValues("email_smtp", "repeated_header").Inc()
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 6, 0},
			Message: "The message has more than one " + name + " line; a message may have only one, so it was not accepted"}
	}
	from := signedFrom(raw)
	var ip net.IP
	if a, ok := ss.conn.Conn().RemoteAddr().(*net.TCPAddr); ok {
		ip = a.IP
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth, signed := authenticate(ctx, ss.s.Resolver, raw, from, ss.from, ss.conn.Hostname(), ip)
	messageID := strings.Trim(strings.TrimSpace(hdr.Header.Get("Message-Id")), "<>")
	for _, rc := range ss.rcpts {
		id := uuid.Must(uuid.NewV7())
		h := sha256.New()
		h.Write(rc.endpoint[:])
		switch {
		case signed != nil:
			// A signed message is the same message whatever is added
			// around it: its key is what the signature covers, so a copy
			// with a new Message-ID or other unsigned lines starts nothing.
			h.Write([]byte("dkim\x00"))
			h.Write(signed)
		case messageID != "":
			h.Write([]byte("message-id\x00" + messageID))
		default:
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
			telemetry.Ingest.WithLabelValues("email_smtp", "deferred").Inc()
			return errTryLater
		}
	}
	telemetry.Ingest.WithLabelValues("email_smtp", "accepted").Inc()
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
