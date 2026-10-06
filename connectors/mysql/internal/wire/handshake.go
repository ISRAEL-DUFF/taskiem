package wire

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // mysql_native_password and RSA-OAEP padding are defined over SHA-1
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"net"
	"sort"
)

// Auth plugin names.
const (
	PluginNative      = "mysql_native_password"
	PluginCachingSHA2 = "caching_sha2_password"
	PluginClear       = "mysql_clear_password"
)

// Config describes how to log in.
type Config struct {
	User     string
	Password string
	Database string // optional
	// TLS, when set, upgrades the connection with an SSLRequest before
	// any credentials are sent; a server without TLS support is refused.
	TLS *tls.Config
	// Attrs are sent as connection attributes when the server accepts them.
	Attrs map[string]string
	// FoundRows makes UPDATE report matched rather than changed rows.
	FoundRows bool
}

// clientCaps are the capabilities this client offers. Never LOCAL_FILES
// or MULTI_STATEMENTS (see the package comment).
const clientCaps = capLongPassword | capLongFlag | capProtocol41 | capTransactions |
	capSecureConnection | capMultiResults | capPSMultiResults | capPluginAuth |
	capPluginAuthLenenc | capConnectAttrs | capDeprecateEOF

// required are the capabilities the server must offer.
const required = capProtocol41 | capSecureConnection

// handshake is the server's Initial Handshake Packet (protocol version 10).
type handshake struct {
	version  string
	connID   uint32
	scramble []byte
	caps     uint32
	charset  byte
	status   uint16
	plugin   string
}

func parseHandshake(p []byte) (handshake, error) {
	var h handshake
	if len(p) == 0 {
		return h, protoErr("empty handshake")
	}
	if p[0] == 0xff {
		return h, parseErrPacket(p)
	}
	if p[0] != 10 {
		return h, fmt.Errorf("%w: unsupported protocol version %d (not a MySQL 4.1+ server?)", ErrConfig, p[0])
	}
	r := reader{b: p[1:]}
	h.version = r.nulString("server version")
	h.connID = r.u32("thread id")
	part1 := r.take(8, "auth-plugin-data-part-1")
	r.u8("filler")
	h.caps = uint32(r.u16("capability flags (lower)"))
	if r.err != nil {
		return h, r.err
	}
	h.scramble = append([]byte(nil), part1...)
	if len(r.b) == 0 {
		return h, nil
	}
	h.charset = r.u8("character set")
	h.status = r.u16("status flags")
	h.caps |= uint32(r.u16("capability flags (upper)")) << 16
	authLen := int(r.u8("auth plugin data length"))
	r.take(10, "reserved")
	if h.caps&capSecureConnection != 0 {
		n := max(13, authLen-8)
		part2 := r.take(n, "auth-plugin-data-part-2")
		// The second part is NUL-terminated; the scramble itself is 20 bytes.
		if len(part2) > 0 && part2[len(part2)-1] == 0 {
			part2 = part2[:len(part2)-1]
		}
		h.scramble = append(h.scramble, part2...)
	}
	if h.caps&capPluginAuth != 0 {
		// Some servers omit the final NUL.
		rest := r.rest()
		for i, c := range rest {
			if c == 0 {
				rest = rest[:i]
				break
			}
		}
		h.plugin = string(rest)
	}
	return h, r.err
}

// Connect performs the connection phase over nc (already dialled through
// the egress guard) and returns a ready connection. On error nc is closed.
func Connect(ctx context.Context, nc net.Conn, cfg Config) (*Conn, error) {
	c := NewConn(nc)
	err := c.run(ctx, func() error { return c.handshake(ctx, cfg) })
	if err != nil {
		_ = c.Abort()
		return nil, err
	}
	return c, nil
}

func (c *Conn) handshake(ctx context.Context, cfg Config) error {
	p, err := c.ReadPacket()
	if err != nil {
		return err
	}
	h, err := parseHandshake(p)
	if err != nil {
		return err
	}
	if h.caps&required != required {
		return fmt.Errorf("%w: server %q lacks CLIENT_PROTOCOL_41/SECURE_CONNECTION", ErrConfig, h.version)
	}
	c.ServerVersion, c.ConnectionID, c.status = h.version, h.connID, h.status
	caps := clientCaps & h.caps
	caps |= required
	if cfg.FoundRows {
		caps |= capFoundRows
	}
	if cfg.Database != "" {
		if h.caps&capConnectWithDB == 0 {
			return fmt.Errorf("%w: server does not accept a database at login", ErrConfig)
		}
		caps |= capConnectWithDB
	}
	if cfg.TLS != nil {
		if h.caps&capSSL == 0 {
			return fmt.Errorf("%w: TLS is required but the server does not offer it", ErrConfig)
		}
		caps |= capSSL
		if err := c.WritePacket(sslRequest(caps)); err != nil {
			return err
		}
		if c.br.Buffered() != 0 {
			// Bytes that arrived before TLS could have been injected by
			// anyone on the path; never treat them as part of the session.
			return protoErr("server sent unencrypted data before the TLS handshake")
		}
		tc := tls.Client(c.nc, cfg.TLS)
		if err := tc.HandshakeContext(ctx); err != nil {
			c.broken = true
			return fmt.Errorf("%w: TLS handshake: %w", ErrConfig, err)
		}
		c.nc = tc
		c.br.Reset(tc)
		c.tls = true
	}
	c.caps = caps

	plugin := h.plugin
	if plugin == "" || caps&capPluginAuth == 0 {
		plugin = PluginNative
	}
	scramble := h.scramble
	if len(scramble) < 20 {
		return protoErr("auth scramble is %d bytes, expected 20", len(scramble))
	}
	auth, err := c.authData(plugin, scramble, cfg.Password)
	if err != nil {
		// An unknown default plugin: answer with ours and let the server
		// switch us to the account's plugin.
		plugin = PluginCachingSHA2
		auth, _ = c.authData(plugin, scramble, cfg.Password)
	}
	resp, err := c.handshakeResponse(cfg, plugin, auth)
	if err != nil {
		return err
	}
	if err := c.WritePacket(resp); err != nil {
		return err
	}
	return c.authLoop(plugin, scramble, cfg.Password)
}

func sslRequest(caps uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, caps)
	b = binary.LittleEndian.AppendUint32(b, MaxPayload)
	b = append(b, charsetUTF8MB4)
	return append(b, make([]byte, 23)...)
}

func (c *Conn) handshakeResponse(cfg Config, plugin string, auth []byte) ([]byte, error) {
	b := binary.LittleEndian.AppendUint32(nil, c.caps)
	b = binary.LittleEndian.AppendUint32(b, MaxPayload)
	b = append(b, charsetUTF8MB4)
	b = append(b, make([]byte, 23)...)
	b = append(append(b, cfg.User...), 0)
	if c.caps&capPluginAuthLenenc != 0 {
		b = AppendLenencBytes(b, auth)
	} else {
		if len(auth) > 255 {
			return nil, fmt.Errorf("%w: auth response too long for a server without CLIENT_PLUGIN_AUTH_LENENC_CLIENT_DATA", ErrConfig)
		}
		b = append(b, byte(len(auth))) // #nosec G115 -- checked above
		b = append(b, auth...)
	}
	if c.caps&capConnectWithDB != 0 {
		b = append(append(b, cfg.Database...), 0)
	}
	if c.caps&capPluginAuth != 0 {
		b = append(append(b, plugin...), 0)
	}
	if c.caps&capConnectAttrs != 0 {
		keys := make([]string, 0, len(cfg.Attrs))
		for k := range cfg.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var attrs []byte
		for _, k := range keys {
			attrs = AppendLenencBytes(attrs, []byte(k))
			attrs = AppendLenencBytes(attrs, []byte(cfg.Attrs[k]))
		}
		b = AppendLenencBytes(b, attrs)
	}
	return b, nil
}

// authData computes the first response for plugin.
func (c *Conn) authData(plugin string, scramble []byte, password string) ([]byte, error) {
	switch plugin {
	case PluginNative:
		return NativePassword(scramble, password), nil
	case PluginCachingSHA2:
		return CachingSHA2Password(scramble, password), nil
	case PluginClear:
		if !c.tls {
			return nil, fmt.Errorf("%w: server asked for mysql_clear_password on a connection without TLS; refusing to send the password", ErrConfig)
		}
		return append([]byte(password), 0), nil
	}
	return nil, fmt.Errorf("%w: unsupported authentication plugin %q", ErrConfig, plugin)
}

// authLoop handles everything after HandshakeResponse41 until OK or ERR.
func (c *Conn) authLoop(plugin string, scramble []byte, password string) error {
	switched, keyRequested := false, false
	for range 8 {
		p, err := c.ReadPacket()
		if err != nil {
			return err
		}
		if len(p) == 0 {
			return protoErr("empty authentication packet")
		}
		switch p[0] {
		case 0x00:
			res, err := parseOK(p)
			if err != nil {
				return err
			}
			c.status = res.Status
			return nil
		case 0xff:
			return parseErrPacket(p)
		case 0xfe: // AuthSwitchRequest
			if switched {
				return protoErr("second authentication switch")
			}
			if len(p) == 1 {
				return fmt.Errorf("%w: server wants the pre-4.1 password scheme, which is insecure and unsupported", ErrConfig)
			}
			switched = true
			r := reader{b: p[1:]}
			plugin = r.nulString("auth switch plugin name")
			data := r.rest()
			if r.err != nil {
				return r.err
			}
			if len(data) > 0 && data[len(data)-1] == 0 {
				data = data[:len(data)-1]
			}
			scramble = append([]byte(nil), data...)
			auth, err := c.authData(plugin, scramble, password)
			if err != nil {
				return err
			}
			if err := c.WritePacket(auth); err != nil {
				return err
			}
		case 0x01: // AuthMoreData
			if plugin != PluginCachingSHA2 {
				return protoErr("unexpected AuthMoreData for plugin %s", plugin)
			}
			data := p[1:]
			switch {
			case keyRequested:
				enc, err := encryptPassword(data, scramble, password)
				if err != nil {
					return err
				}
				keyRequested = false
				if err := c.WritePacket(enc); err != nil {
					return err
				}
			case len(data) == 1 && data[0] == 3: // fast auth succeeded; OK follows
			case len(data) == 1 && data[0] == 4: // full authentication
				if c.tls {
					if err := c.WritePacket(append([]byte(password), 0)); err != nil {
						return err
					}
				} else {
					keyRequested = true
					if err := c.WritePacket([]byte{2}); err != nil { // request public key
						return err
					}
				}
			default:
				return protoErr("unexpected caching_sha2_password data (% x)", data)
			}
		default:
			return protoErr("unexpected packet 0x%02x during authentication", p[0])
		}
	}
	return protoErr("authentication did not finish")
}

// NativePassword is the mysql_native_password response:
// SHA1(password) XOR SHA1(scramble + SHA1(SHA1(password))).
func NativePassword(scramble []byte, password string) []byte {
	if password == "" {
		return []byte{}
	}
	stage1 := sha1.Sum([]byte(password)) //nolint:gosec // defined by the protocol
	stage2 := sha1.Sum(stage1[:])        //nolint:gosec // defined by the protocol
	h := sha1.New()                      //nolint:gosec // defined by the protocol
	h.Write(scramble[:20])
	h.Write(stage2[:])
	out := h.Sum(nil)
	subtle.XORBytes(out, out, stage1[:])
	return out
}

// CachingSHA2Password is the caching_sha2_password fast-auth response:
// SHA256(password) XOR SHA256(SHA256(SHA256(password)) + nonce).
func CachingSHA2Password(scramble []byte, password string) []byte {
	if password == "" {
		return []byte{}
	}
	m1 := sha256.Sum256([]byte(password))
	m2 := sha256.Sum256(m1[:])
	h := sha256.New()
	h.Write(m2[:])
	h.Write(scramble[:20])
	out := h.Sum(nil)
	subtle.XORBytes(out, out, m1[:])
	return out
}

// encryptPassword encrypts (password + NUL) XOR the scramble, repeated,
// with the server's RSA public key and OAEP padding.
func encryptPassword(keyPEM, scramble []byte, password string) ([]byte, error) {
	blk, _ := pem.Decode(keyPEM)
	if blk == nil {
		return nil, protoErr("server public key is not PEM")
	}
	var pub *rsa.PublicKey
	if k, err := x509.ParsePKIXPublicKey(blk.Bytes); err == nil {
		pk, ok := k.(*rsa.PublicKey)
		if !ok {
			return nil, protoErr("server public key is not RSA")
		}
		pub = pk
	} else if pk, err2 := x509.ParsePKCS1PublicKey(blk.Bytes); err2 == nil {
		pub = pk
	} else {
		return nil, protoErr("server public key: %v", err)
	}
	msg := append([]byte(password), 0)
	for i := range msg {
		msg[i] ^= scramble[i%len(scramble)]
	}
	out, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, pub, msg, nil) //nolint:gosec // OAEP with SHA-1 is what the server expects
	if err != nil {
		return nil, fmt.Errorf("%w: encrypt password: %w", ErrConfig, err)
	}
	return out, nil
}
