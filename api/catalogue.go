package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/catalogue"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/wasmconn"
)

// The public connector catalogue (Phase 4 P4-6, decision 0020;
// docs/connector-submissions.md): publishers submit signed packages, the
// API runs the automated checks, a reviewer outside the publisher decides
// (taskiem catalogue review, an operator's CLI), and tenants install
// published versions, pinned, with consent to their hosts and writes.

// --- publishers ---

type publisherInfo struct {
	Slug        string     `json:"slug"`
	Name        string     `json:"name"`
	PublicKey   string     `json:"public_key"`
	KeyID       string     `json:"key_id"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	Note        *string    `json:"note,omitempty"`
}

func readPublisher(ctx context.Context, tx pgx.Tx) (*publisherInfo, error) {
	var p publisherInfo
	var key []byte
	err := tx.QueryRow(ctx, `SELECT slug, name, public_key, key_id, status, requested_at, verified_at, status_note FROM connector_publishers`).
		Scan(&p.Slug, &p.Name, &key, &p.KeyID, &p.Status, &p.RequestedAt, &p.VerifiedAt, &p.Note)
	if err != nil {
		return nil, err
	}
	p.PublicKey = base64.StdEncoding.EncodeToString(key)
	return &p, nil
}

func (s *Server) getPublisher(w http.ResponseWriter, r *http.Request) {
	var p *publisherInfo
	err := s.tx(r, func(tx pgx.Tx) (err error) {
		p, err = readPublisher(r.Context(), tx)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// putPublisher asks for a namespace, or renames the publisher or rotates
// its signing key. The slug never changes; an operator verifies it.
func (s *Server) putPublisher(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
	}
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 100 {
		s.fail(w, r, fmt.Errorf("%w: name is required (at most 100 characters)", errBadRequest))
		return
	}
	pub, err := connpkg.ParsePublicKey(body.PublicKey)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	p := principalFrom(r.Context())
	status := http.StatusOK
	err = s.tx(r, func(tx pgx.Tx) error {
		cur, err := readPublisher(r.Context(), tx)
		if errors.Is(err, pgx.ErrNoRows) {
			if !catalogue.SlugRe.MatchString(body.Slug) || catalogue.Reserved[body.Slug] {
				return fmt.Errorf("%w: slug %q: 2 to 30 lower-case letters and digits, starting with a letter, and not a reserved word", errBadRequest, body.Slug)
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO connector_publishers (tenant_id, slug, name, public_key, key_id, requested_by) VALUES ($1, $2, $3, $4, $5, $6)`,
				p.TenantID, body.Slug, body.Name, []byte(pub), connpkg.KeyID(pub), p.Actor()); err != nil {
				var pgErr interface{ SQLState() string }
				if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
					return fmt.Errorf("%w: the namespace %q is taken", errConflict, body.Slug)
				}
				return err
			}
			status = http.StatusCreated
			return auditTx(r, tx, "catalogue.publisher.request", body.Slug, map[string]any{"name": body.Name, "key_id": connpkg.KeyID(pub)})
		}
		if err != nil {
			return err
		}
		if body.Slug != "" && body.Slug != cur.Slug {
			return fmt.Errorf("%w: this organisation publishes as %q; a namespace never changes", errConflict, cur.Slug)
		}
		rotated := cur.KeyID != connpkg.KeyID(pub)
		if _, err := tx.Exec(r.Context(), `UPDATE connector_publishers SET name = $1, public_key = $2, key_id = $3,
			key_rotated_at = CASE WHEN $4 THEN now() ELSE key_rotated_at END`, body.Name, []byte(pub), connpkg.KeyID(pub), rotated); err != nil {
			return err
		}
		return auditTx(r, tx, "catalogue.publisher.update", cur.Slug, map[string]any{"name": body.Name, "key_id": connpkg.KeyID(pub), "key_rotated": rotated})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out *publisherInfo
	_ = s.tx(r, func(tx pgx.Tx) (err error) {
		out, err = readPublisher(r.Context(), tx)
		return err
	})
	writeJSON(w, status, out)
}

// --- submissions ---

type submissionInfo struct {
	ID          uuid.UUID       `json:"id"`
	Connector   string          `json:"connector"`
	Version     string          `json:"version"`
	State       string          `json:"state"`
	Licence     string          `json:"licence"`
	Digest      string          `json:"digest"`
	SubmittedBy string          `json:"submitted_by"`
	SubmittedAt time.Time       `json:"submitted_at"`
	ReviewedBy  *string         `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time      `json:"reviewed_at,omitempty"`
	ReviewNote  *string         `json:"review_note,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	RevokedAt   *time.Time      `json:"revoked_at,omitempty"`
	Revoke      *string         `json:"revoke_reason,omitempty"`
	Checks      json.RawMessage `json:"checks,omitempty"`
	Events      []catalogueStep `json:"events,omitempty"`
}

type catalogueStep struct {
	Event  string          `json:"event"`
	Actor  string          `json:"actor"`
	Note   *string         `json:"note,omitempty"`
	Detail json.RawMessage `json:"detail,omitempty"`
	At     time.Time       `json:"at"`
}

const submissionCols = `id, connector_id, version, state, licence, encode(package_digest, 'hex'), submitted_by, submitted_at, reviewed_by, reviewed_at,
	review_note, published_at, revoked_at, revoke_reason`

func scanSubmission(row pgx.Row, extra ...any) (submissionInfo, error) {
	var s submissionInfo
	dest := append([]any{&s.ID, &s.Connector, &s.Version, &s.State, &s.Licence, &s.Digest, &s.SubmittedBy, &s.SubmittedAt, &s.ReviewedBy,
		&s.ReviewedAt, &s.ReviewNote, &s.PublishedAt, &s.RevokedAt, &s.Revoke}, extra...)
	return s, row.Scan(dest...)
}

func (s *Server) listSubmissions(w http.ResponseWriter, r *http.Request) {
	var out []submissionInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+submissionCols+` FROM catalogue_versions ORDER BY submitted_at DESC LIMIT 500`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sub, err := scanSubmission(rows)
			if err != nil {
				return err
			}
			out = append(out, sub)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": nonNil(out)})
}

func (s *Server) getSubmission(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var sub submissionInfo
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		var checks json.RawMessage
		if sub, err = scanSubmission(tx.QueryRow(r.Context(), `SELECT `+submissionCols+`, checks FROM catalogue_versions WHERE id = $1`, id), &checks); err != nil {
			return err
		}
		sub.Checks = checks
		rows, err := tx.Query(r.Context(), `SELECT event, actor, note, detail, at FROM catalogue_events WHERE version_id = $1 ORDER BY id`, id)
		if err != nil {
			return err
		}
		sub.Events, err = pgx.CollectRows(rows, pgx.RowToStructByPos[catalogueStep])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// submitPackage takes a taskiem-connector-package/v1 document, runs the
// automated checks, and stores the version as in_review or checks_failed.
func (s *Server) submitPackage(w http.ResponseWriter, r *http.Request) {
	if s.Catalogue == nil {
		writeErr(w, http.StatusNotImplemented, "this deployment does not take catalogue submissions")
		return
	}
	pkg, err := connpkg.Read(http.MaxBytesReader(w, r.Body, connpkg.MaxSize))
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: %w", errBadRequest, err))
		return
	}
	p := principalFrom(r.Context())
	var pub *publisherInfo
	var published []catalogue.Published
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		if pub, err = readPublisher(r.Context(), tx); errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: register a publisher namespace first (PUT /v1/catalogue/publisher)", errForbidden)
		} else if err != nil {
			return err
		}
		if pub.Status != "verified" {
			return fmt.Errorf("%w: the namespace %q is %s; Taskiem verifies publishers before they submit", errForbidden, pub.Slug, pub.Status)
		}
		rows, err := tx.Query(r.Context(), `SELECT version, manifest, state FROM catalogue_versions WHERE connector_id = $1 AND state IN ('published', 'revoked', 'approved', 'in_review')`, pkg.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v, m, state string
			if err := rows.Scan(&v, &m, &state); err != nil {
				return err
			}
			if v == pkg.Version {
				return fmt.Errorf("%w: %s %s is already submitted; versions never change, so raise the version", errConflict, pkg.ID, pkg.Version)
			}
			if mm, probs := connector.Parse([]byte(m)); len(probs) == 0 && (state == "published" || state == "revoked") {
				published = append(published, catalogue.Published{Version: v, Manifest: mm})
			}
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key, _ := connpkg.ParsePublicKey(pub.PublicKey)
	rep := s.Catalogue.Run(r.Context(), pkg, pub.Slug, key, published)
	state, event := "in_review", "checks_passed"
	if !rep.Passed {
		state, event = "checks_failed", "checks_failed"
	}
	if !strings.HasPrefix(pkg.ID, connector.CataloguePrefix+pub.Slug+"_") || !catalogueIDRe(pkg.ID) || !versionRe(pkg.Version) {
		// The row could not be stored; the report says why.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the package is not in your namespace or has no valid id and version", "checks": rep})
		return
	}
	id := uuid.Must(uuid.NewV7())
	checks, _ := json.Marshal(rep)
	suite, _ := json.Marshal(pkg.Conformance)
	att, _ := json.Marshal(pkg.Attestation)
	pkgDigest, _ := hex.DecodeString(pkg.Digest)
	modDigest, _ := hex.DecodeString(pkg.ModuleDigest())
	sig, _ := base64.StdEncoding.DecodeString(pkg.Signature)
	if len(pkgDigest) != 32 || len(sig) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the package is not signed", "checks": rep})
		return
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO catalogue_versions (id, publisher_tenant, publisher, connector_id, version, manifest, module, module_digest,
			package_digest, key_id, signature, licence, source_url, conformance, attestation, checks, state, submitted_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14, $15, $16, $17, $18)`,
			id, p.TenantID, pub.Slug, pkg.ID, pkg.Version, pkg.Manifest, pkg.Module, modDigest, pkgDigest, pkg.KeyID, sig, pkg.Licence, pkg.SourceURL,
			suite, att, checks, state, p.Actor()); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
				return fmt.Errorf("%w: %s %s is already submitted; versions never change, so raise the version", errConflict, pkg.ID, pkg.Version)
			}
			return err
		}
		for _, ev := range []struct{ event, note string }{{"submitted", ""}, {event, strings.Join(rep.Failed(), ", ")}} {
			if _, err := tx.Exec(r.Context(), `INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note) VALUES ($1, $2, $3, $4, NULLIF($5, ''))`,
				id, p.TenantID, ev.event, p.Actor(), ev.note); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "catalogue.submit", pkg.ID+"@"+pkg.Version, map[string]any{"submission": id, "digest": pkg.Digest, "state": state})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "connector": pkg.ID, "version": pkg.Version, "state": state, "digest": pkg.Digest, "checks": rep})
}

// moveSubmission is the publisher's own transitions: withdraw, publish an
// approved version, revoke a published one.
func (s *Server) moveSubmission(to string) http.HandlerFunc {
	from := map[string][]string{"withdrawn": {"in_review", "checks_failed", "approved"}, "published": {"approved"}, "revoked": {"published"}}[to]
	action := map[string]string{"withdrawn": "withdraw", "published": "publish", "revoked": "revoke"}[to]
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			s.fail(w, r, pgx.ErrNoRows)
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		body.Reason = strings.TrimSpace(body.Reason)
		if to == "revoked" && body.Reason == "" {
			s.fail(w, r, fmt.Errorf("%w: say why the version is revoked; installing tenants are told", errBadRequest))
			return
		}
		p := principalFrom(r.Context())
		var conn, version string
		err = s.tx(r, func(tx pgx.Tx) error {
			var state string
			if err := tx.QueryRow(r.Context(), `SELECT connector_id, version, state FROM catalogue_versions WHERE id = $1 FOR UPDATE`, id).Scan(&conn, &version, &state); err != nil {
				return err
			}
			ok := false
			for _, f := range from {
				ok = ok || f == state
			}
			if !ok {
				return fmt.Errorf("%w: %s %s is %s; it cannot be %s", errConflict, conn, version, state, to)
			}
			if _, err := tx.Exec(r.Context(), `UPDATE catalogue_versions SET state = $2,
				published_at = CASE WHEN $2 = 'published' THEN now() ELSE published_at END,
				revoked_at = CASE WHEN $2 = 'revoked' THEN now() ELSE revoked_at END,
				revoked_by = CASE WHEN $2 = 'revoked' THEN $3 ELSE revoked_by END,
				revoke_reason = CASE WHEN $2 = 'revoked' THEN $4 ELSE revoke_reason END WHERE id = $1`, id, to, p.Actor(), body.Reason); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note) VALUES ($1, $2, $3, $4, NULLIF($5, ''))`,
				id, p.TenantID, to, p.Actor(), body.Reason); err != nil {
				return err
			}
			return auditTx(r, tx, "catalogue."+action, conn+"@"+version, map[string]any{"submission": id, "reason": body.Reason})
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out := map[string]any{"id": id, "connector": conn, "version": version, "state": to}
		if to == "revoked" {
			n, err := catalogue.NotifyRevoked(r.Context(), s.Store.Pool, conn, version, body.Reason, "publisher:"+p.TenantID.String(), s.PublicURL)
			if err != nil {
				s.Logger.Error("catalogue revocation alerts", "connector", conn, "version", version, "err", err)
			}
			out["tenants_alerted"] = n
		}
		if s.Connectors != nil {
			s.Connectors.ForgetAll()
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// --- the catalogue ---

type catalogueVersion struct {
	catalogue.Summary
	Publisher     string     `json:"publisher"`
	PublisherName string     `json:"publisher_name"`
	Licence       string     `json:"licence"`
	SourceURL     *string    `json:"source_url,omitempty"`
	Digest        string     `json:"digest"`
	State         string     `json:"state,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokeReason  *string    `json:"revoke_reason,omitempty"`
	Consent       any        `json:"consent"`
}

type catalogueEntry struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Category      string             `json:"category"`
	Publisher     string             `json:"publisher"`
	PublisherName string             `json:"publisher_name"`
	Versions      []catalogueVersion `json:"versions"`  // newest first
	Installed     map[string]string  `json:"installed"` // major -> version
}

func (s *Server) listCatalogue(w http.ResponseWriter, r *http.Request) {
	byID := map[string]*catalogueEntry{}
	installed := map[string]map[string]string{}
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT connector_id, version, publisher, publisher_name, manifest, licence, source_url, package_digest, published_at FROM taskiem_catalogue_listing()`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, version, pubSlug, pubName, manifest string
			v := catalogueVersion{State: "published"}
			if err := rows.Scan(&id, &version, &pubSlug, &pubName, &manifest, &v.Licence, &v.SourceURL, &v.Digest, &v.PublishedAt); err != nil {
				return err
			}
			m, probs := connector.Parse([]byte(manifest))
			if len(probs) > 0 {
				continue
			}
			v.Summary, v.Publisher, v.PublisherName, v.Consent = catalogue.Summarise(m), pubSlug, pubName, catalogue.ConsentFor(m)
			e := byID[id]
			if e == nil {
				e = &catalogueEntry{ID: id, Publisher: pubSlug, PublisherName: pubName, Installed: map[string]string{}}
				byID[id] = e
			}
			e.Versions = append(e.Versions, v)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		irows, err := tx.Query(r.Context(), `SELECT connector_id, major, version FROM catalogue_installs`)
		if err != nil {
			return err
		}
		defer irows.Close()
		for irows.Next() {
			var id, version string
			var major int
			if err := irows.Scan(&id, &major, &version); err != nil {
				return err
			}
			if installed[id] == nil {
				installed[id] = map[string]string{}
			}
			installed[id][strconv.Itoa(major)] = version
		}
		return irows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]*catalogueEntry, 0, len(byID))
	for id, e := range byID {
		sort.Slice(e.Versions, func(i, j int) bool { return wasmconn.Newer(e.Versions[i].Version, e.Versions[j].Version) })
		newest := e.Versions[0]
		e.Name, e.Description, e.Category = newest.Name, newest.Description, newest.Category
		if installed[id] != nil {
			e.Installed = installed[id]
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"connectors": out})
}

// catalogueVersionOf reads one published or revoked version.
func catalogueVersionOf(ctx context.Context, tx pgx.Tx, id, version string) (*catalogueVersion, *connector.Manifest, error) {
	var v catalogueVersion
	var manifest, conn, ver, modDigest string
	err := tx.QueryRow(ctx, `SELECT connector_id, version, publisher, publisher_name, state, manifest, licence, source_url, package_digest, module_digest,
		published_at, revoked_at, revoke_reason FROM taskiem_catalogue_version($1, $2)`, id, version).
		Scan(&conn, &ver, &v.Publisher, &v.PublisherName, &v.State, &manifest, &v.Licence, &v.SourceURL, &v.Digest, &modDigest, &v.PublishedAt, &v.RevokedAt, &v.RevokeReason)
	if err != nil {
		return nil, nil, err
	}
	m, probs := connector.Parse([]byte(manifest))
	if len(probs) > 0 {
		return nil, nil, fmt.Errorf("stored manifest of %s %s: %s", id, version, strings.Join(probs, "; "))
	}
	v.Summary, v.Consent = catalogue.Summarise(m), catalogue.ConsentFor(m)
	return &v, m, nil
}

func (s *Server) getCatalogueVersion(w http.ResponseWriter, r *http.Request) {
	var v *catalogueVersion
	err := s.tx(r, func(tx pgx.Tx) (err error) {
		v, _, err = catalogueVersionOf(r.Context(), tx, chi.URLParam(r, "id"), chi.URLParam(r, "version"))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// --- installs ---

type installInfo struct {
	Connector   string            `json:"connector"`
	Major       int               `json:"major"`
	Ref         string            `json:"ref"`
	Version     string            `json:"version"`
	State       string            `json:"state"` // published, revoked, unavailable
	Reason      *string           `json:"revoke_reason,omitempty"`
	Latest      string            `json:"latest,omitempty"` // newest published version of the major, when newer
	Consent     catalogue.Consent `json:"consent"`
	InstalledBy string            `json:"installed_by"`
	InstalledAt time.Time         `json:"installed_at"`
	UpdatedAt   *time.Time        `json:"updated_at,omitempty"`
}

func (s *Server) listInstalls(w http.ResponseWriter, r *http.Request) {
	var out []installInfo
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT connector_id, major, version, consent, installed_by, installed_at, updated_at FROM catalogue_installs ORDER BY connector_id, major`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var in installInfo
			var consent []byte
			if err := rows.Scan(&in.Connector, &in.Major, &in.Version, &consent, &in.InstalledBy, &in.InstalledAt, &in.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			_ = json.Unmarshal(consent, &in.Consent)
			in.Ref = in.Connector + "@" + strconv.Itoa(in.Major)
			out = append(out, in)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		latest := map[string]string{}
		lrows, err := tx.Query(r.Context(), `SELECT connector_id, version FROM taskiem_catalogue_listing()`)
		if err != nil {
			return err
		}
		for lrows.Next() {
			var id, v string
			if err := lrows.Scan(&id, &v); err != nil {
				lrows.Close()
				return err
			}
			ref := id + "@" + strings.SplitN(v, ".", 2)[0]
			if cur, ok := latest[ref]; !ok || wasmconn.Newer(v, cur) {
				latest[ref] = v
			}
		}
		lrows.Close()
		for i := range out {
			in := &out[i]
			in.State = "unavailable"
			if v, _, err := catalogueVersionOf(r.Context(), tx, in.Connector, in.Version); err == nil {
				in.State, in.Reason = v.State, v.RevokeReason
			}
			if l := latest[in.Ref]; l != "" && wasmconn.Newer(l, in.Version) {
				in.Latest = l
			}
		}
		return lrows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installs": nonNil(out)})
}

type installBody struct {
	Connector string             `json:"connector"`
	Version   string             `json:"version"`
	Consent   *catalogue.Consent `json:"consent"`
}

// installConnector pins a published version. The body's consent must
// cover the version's hosts and writes: the client shows them and echoes
// what the person agreed to.
func (s *Server) installConnector(w http.ResponseWriter, r *http.Request) {
	var body installBody
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var ref string
	err := s.tx(r, func(tx pgx.Tx) error {
		v, m, err := catalogueVersionOf(r.Context(), tx, body.Connector, body.Version)
		if err != nil {
			return err
		}
		if v.State != "published" {
			return fmt.Errorf("%w: %s %s is %s", errConflict, body.Connector, body.Version, v.State)
		}
		want := catalogue.ConsentFor(m)
		if body.Consent == nil || !body.Consent.Covers(want) {
			return fmt.Errorf("%w: consent to the version's hosts (%s) and writes is required", errBadRequest, strings.Join(want.Hosts, ", "))
		}
		consent, _ := json.Marshal(want)
		major, _ := strconv.Atoi(strings.SplitN(body.Version, ".", 2)[0])
		ref = body.Connector + "@" + strconv.Itoa(major)
		if _, err := tx.Exec(r.Context(), `INSERT INTO catalogue_installs (tenant_id, connector_id, major, version, consent, installed_by) VALUES ($1, $2, $3, $4, $5, $6)`,
			p.TenantID, body.Connector, major, body.Version, consent, p.Actor()); err != nil {
			var pgErr interface{ SQLState() string }
			if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
				return fmt.Errorf("%w: %s is already installed; upgrade it instead", errConflict, ref)
			}
			return err
		}
		return auditTx(r, tx, "catalogue.install", body.Connector+"@"+body.Version, map[string]any{"digest": v.Digest, "consent": want})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Connectors != nil {
		s.Connectors.Forget(p.TenantID.String())
	}
	writeJSON(w, http.StatusCreated, map[string]any{"connector": body.Connector, "version": body.Version, "ref": ref})
}

// upgradeDiff reads an install and diffs it with the version it would
// move to.
func (s *Server) upgradeDiff(ctx context.Context, tx pgx.Tx, id string, major int, to string) (cur string, d catalogue.Diff, next *catalogueVersion, nm *connector.Manifest, err error) {
	if err = tx.QueryRow(ctx, `SELECT version FROM catalogue_installs WHERE connector_id = $1 AND major = $2`, id, major).Scan(&cur); err != nil {
		return
	}
	if !strings.HasPrefix(to, strconv.Itoa(major)+".") {
		err = fmt.Errorf("%w: %s is not in major %d; install the new major alongside instead", errBadRequest, to, major)
		return
	}
	_, om, oerr := catalogueVersionOf(ctx, tx, id, cur)
	if next, nm, err = catalogueVersionOf(ctx, tx, id, to); err != nil {
		return
	}
	if next.State != "published" {
		err = fmt.Errorf("%w: %s %s is %s", errConflict, id, to, next.State)
		return
	}
	if oerr != nil {
		// The installed version is gone from the catalogue: everything
		// the new one asks for is new.
		d = catalogue.Diff{From: cur, To: to, AddedHosts: nm.Hosts(), NeedsConsent: true}
		return
	}
	d = catalogue.Compare(om, nm)
	return
}

func (s *Server) getUpgrade(w http.ResponseWriter, r *http.Request) {
	major, _ := strconv.Atoi(chi.URLParam(r, "major"))
	var d catalogue.Diff
	var next *catalogueVersion
	err := s.tx(r, func(tx pgx.Tx) (err error) {
		_, d, next, _, err = s.upgradeDiff(r.Context(), tx, chi.URLParam(r, "id"), major, r.URL.Query().Get("to"))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": d, "version": next})
}

func (s *Server) upgradeInstall(w http.ResponseWriter, r *http.Request) {
	major, _ := strconv.Atoi(chi.URLParam(r, "major"))
	id := chi.URLParam(r, "id")
	var body installBody
	if err := decodeBody(r, &body); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var d catalogue.Diff
	err := s.tx(r, func(tx pgx.Tx) error {
		_, diff, next, nm, err := s.upgradeDiff(r.Context(), tx, id, major, body.Version)
		if err != nil {
			return err
		}
		d = diff
		if d.From == d.To {
			return fmt.Errorf("%w: %s %s is already installed", errConflict, id, d.To)
		}
		want := catalogue.ConsentFor(nm)
		if d.NeedsConsent && (body.Consent == nil || !body.Consent.Covers(want)) {
			return fmt.Errorf("%w: %s %s adds hosts or writes or changes a class: consent to its hosts (%s) and writes is required", errBadRequest, id, body.Version, strings.Join(want.Hosts, ", "))
		}
		consent, _ := json.Marshal(want)
		if _, err := tx.Exec(r.Context(), `UPDATE catalogue_installs SET version = $3, consent = $4, updated_by = $5, updated_at = now() WHERE connector_id = $1 AND major = $2`,
			id, major, body.Version, consent, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "catalogue.upgrade", id+"@"+body.Version, map[string]any{"from": d.From, "digest": next.Digest, "diff": d})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Connectors != nil {
		s.Connectors.Forget(p.TenantID.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{"connector": id, "version": d.To, "diff": d})
}

func (s *Server) uninstallConnector(w http.ResponseWriter, r *http.Request) {
	major, _ := strconv.Atoi(chi.URLParam(r, "major"))
	id := chi.URLParam(r, "id")
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		var version string
		if err := tx.QueryRow(r.Context(), `DELETE FROM catalogue_installs WHERE connector_id = $1 AND major = $2 RETURNING version`, id, major).Scan(&version); err != nil {
			return err
		}
		return auditTx(r, tx, "catalogue.uninstall", id+"@"+version, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Connectors != nil {
		s.Connectors.Forget(p.TenantID.String())
	}
	w.WriteHeader(http.StatusNoContent)
}

func catalogueIDRe(id string) bool {
	_, _, ok := catalogue.ParseID(id)
	return ok
}

func versionRe(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil || p == "" {
			return false
		}
	}
	return true
}
