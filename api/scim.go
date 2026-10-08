package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SCIM 2.0 provisioning (spec 13.2; RFC 7643, RFC 7644). The identity
// provider authenticates with an API key holding scim.provision. It
// creates, updates and deactivates members, and moves them between groups;
// which roles a group carries is set by an administrator (GET/PUT
// /v1/scim), held to the no-escalation rule, so the key itself grants
// nothing an administrator has not approved. SCIM manages only the
// memberships it created, except that deactivating someone removes all of
// theirs. Owners are never deprovisioned by SCIM.

const (
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrSchema   = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimPatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimMaxResults  = 200
)

type scimError struct {
	status int
	typ    string // scimType (RFC 7644 3.12), optional
	detail string
}

func (e *scimError) Error() string { return e.detail }

func scimErr(status int, typ, format string, args ...any) error {
	return &scimError{status: status, typ: typ, detail: fmt.Sprintf(format, args...)}
}

func writeSCIM(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) scimFail(w http.ResponseWriter, r *http.Request, err error) {
	var se *scimError
	switch {
	case errors.As(err, &se):
	case errors.Is(err, pgx.ErrNoRows):
		se = &scimError{status: http.StatusNotFound, detail: "not found"}
	case errors.Is(err, errBadRequest):
		se = &scimError{status: http.StatusBadRequest, typ: "invalidValue", detail: strings.TrimPrefix(err.Error(), "bad request: ")}
	case errors.Is(err, errConflict):
		se = &scimError{status: http.StatusConflict, detail: strings.TrimPrefix(err.Error(), "conflict: ")}
	default:
		s.Logger.Error("scim request failed", "path", r.URL.Path, "err", err)
		se = &scimError{status: http.StatusInternalServerError, detail: "internal error"}
	}
	body := map[string]any{"schemas": []string{scimErrSchema}, "status": strconv.Itoa(se.status), "detail": se.detail}
	if se.typ != "" {
		body["scimType"] = se.typ
	}
	writeSCIM(w, se.status, body)
}

// SCIM is the /scim/v2 router.
func (s *Server) SCIM() http.Handler {
	r := chi.NewRouter()
	r.Use(s.scimAuth)
	r.Get("/ServiceProviderConfig", s.scimServiceProviderConfig)
	r.Get("/ResourceTypes", s.scimResourceTypes)
	r.Get("/Users", s.scimListUsers)
	r.Post("/Users", s.scimCreateUser)
	r.Get("/Users/{id}", s.scimGetUser)
	r.Put("/Users/{id}", s.scimPutUser)
	r.Patch("/Users/{id}", s.scimPatchUser)
	r.Delete("/Users/{id}", s.scimDeleteUser)
	r.Get("/Groups", s.scimListGroups)
	r.Post("/Groups", s.scimCreateGroup)
	r.Get("/Groups/{id}", s.scimGetGroup)
	r.Put("/Groups/{id}", s.scimPutGroup)
	r.Patch("/Groups/{id}", s.scimPatchGroup)
	r.Delete("/Groups/{id}", s.scimDeleteGroup)
	return r
}

// scimAuth admits API keys holding scim.provision, never sessions.
func (s *Server) scimAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+apiKeyPrefix) {
			s.scimFail(w, r, scimErr(http.StatusUnauthorized, "", "a bearer API key is required"))
			return
		}
		p, err := s.resolve(r)
		if err != nil || p == nil {
			s.scimFail(w, r, scimErr(http.StatusUnauthorized, "", "authentication required"))
			return
		}
		if !p.Can(PermSCIM) {
			s.scimFail(w, r, scimErr(http.StatusForbidden, "", "requires %s", PermSCIM))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func (s *Server) scimLocation(kind string, id uuid.UUID) string {
	return strings.TrimRight(s.PublicURL, "/") + "/scim/v2/" + kind + "/" + id.String()
}

func (s *Server) scimServiceProviderConfig(w http.ResponseWriter, _ *http.Request) {
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":               []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"patch":                 map[string]any{"supported": true},
		"bulk":                  map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":                map[string]any{"supported": true, "maxResults": scimMaxResults},
		"changePassword":        map[string]any{"supported": false},
		"sort":                  map[string]any{"supported": false},
		"etag":                  map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{"type": "oauthbearertoken", "name": "Bearer token", "description": "A Taskiem API key holding scim.provision"}},
	})
}

func (s *Server) scimResourceTypes(w http.ResponseWriter, _ *http.Request) {
	rt := func(name, endpoint, schema string) map[string]any {
		return map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": name, "name": name, "endpoint": endpoint, "schema": schema}
	}
	list := []any{rt("User", "/Users", scimUserSchema), rt("Group", "/Groups", scimGroupSchema)}
	writeSCIM(w, http.StatusOK, map[string]any{"schemas": []string{scimListSchema}, "totalResults": len(list), "startIndex": 1, "itemsPerPage": len(list), "Resources": list})
}

// --- reading requests ---

func scimDecode(r *http.Request, v any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return scimErr(http.StatusRequestEntityTooLarge, "", "request too large")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return scimErr(http.StatusBadRequest, "invalidSyntax", "%v", err)
	}
	return nil
}

// flexBool reads a boolean that some providers send as a string ("False").
type flexBool bool

func (b *flexBool) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case bool:
		*b = flexBool(x)
	case string:
		p, err := strconv.ParseBool(strings.ToLower(x))
		if err != nil {
			return fmt.Errorf("not a boolean: %q", x)
		}
		*b = flexBool(p)
	default:
		return fmt.Errorf("not a boolean: %s", raw)
	}
	return nil
}

var scimFilterRe = regexp.MustCompile(`^\s*([A-Za-z][\w.]*)\s+(?i:eq)\s+"((?:[^"\\]|\\.)*)"\s*$`)

// scimFilter reads the one filter form providers use to look people up:
// attribute eq "value".
func scimFilter(r *http.Request, allowed map[string]string) (col, val string, err error) {
	f := r.URL.Query().Get("filter")
	if f == "" {
		return "", "", nil
	}
	m := scimFilterRe.FindStringSubmatch(f)
	if m == nil {
		return "", "", scimErr(http.StatusBadRequest, "invalidFilter", "only `attribute eq \"value\"` filters are supported")
	}
	for attr, c := range allowed {
		if strings.EqualFold(attr, m[1]) {
			var v string
			if err := json.Unmarshal([]byte(`"`+m[2]+`"`), &v); err != nil {
				return "", "", scimErr(http.StatusBadRequest, "invalidFilter", "bad value")
			}
			return c, v, nil
		}
	}
	return "", "", scimErr(http.StatusBadRequest, "invalidFilter", "cannot filter on %s", m[1])
}

func scimPage(r *http.Request) (start, count int) {
	start, count = 1, 100
	if v, err := strconv.Atoi(r.URL.Query().Get("startIndex")); err == nil && v > 1 {
		start = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && v >= 0 {
		count = min(v, scimMaxResults)
	}
	return start, count
}

func scimID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, scimErr(http.StatusNotFound, "", "not found")
	}
	return id, nil
}

// --- users ---

type scimUserIn struct {
	UserName    string    `json:"userName"`
	ExternalID  string    `json:"externalId"`
	DisplayName string    `json:"displayName"`
	Active      *flexBool `json:"active"`
	Name        struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
		Formatted  string `json:"formatted"`
	} `json:"name"`
	Emails []struct {
		Value   string   `json:"value"`
		Primary flexBool `json:"primary"`
	} `json:"emails"`
}

// email is the person's sign-in email: the primary email, else the first,
// else the user name when it is an email.
func (u *scimUserIn) email() string {
	for _, e := range u.Emails {
		if e.Primary && e.Value != "" {
			return e.Value
		}
	}
	if len(u.Emails) > 0 && u.Emails[0].Value != "" {
		return u.Emails[0].Value
	}
	return u.UserName
}

type scimUser struct {
	ID          uuid.UUID
	UserName    string
	Email       string
	ExternalID  *string
	DisplayName string
	GivenName   string
	FamilyName  string
	Active      bool
	Created     time.Time
	Modified    time.Time
	groups      []scimRef
}

type scimRef struct {
	Value   uuid.UUID `json:"value"`
	Display string    `json:"display"`
	Ref     string    `json:"$ref"`
}

const scimUserCols = `user_id, user_name, email, external_id, display_name, given_name, family_name, active, created_at, updated_at`

func scanSCIMUser(row pgx.Row) (*scimUser, error) {
	var u scimUser
	err := row.Scan(&u.ID, &u.UserName, &u.Email, &u.ExternalID, &u.DisplayName, &u.GivenName, &u.FamilyName, &u.Active, &u.Created, &u.Modified)
	return &u, err
}

func (s *Server) scimUserJSON(u *scimUser) map[string]any {
	out := map[string]any{
		"schemas":     []string{scimUserSchema},
		"id":          u.ID,
		"userName":    u.UserName,
		"displayName": u.DisplayName,
		"name":        map[string]any{"givenName": u.GivenName, "familyName": u.FamilyName, "formatted": strings.TrimSpace(u.GivenName + " " + u.FamilyName)},
		"emails":      []map[string]any{{"value": u.Email, "primary": true, "type": "work"}},
		"active":      u.Active,
		"groups":      u.groups,
		"meta":        map[string]any{"resourceType": "User", "created": u.Created, "lastModified": u.Modified, "location": s.scimLocation("Users", u.ID)},
	}
	if u.groups == nil {
		out["groups"] = []scimRef{}
	}
	if u.ExternalID != nil {
		out["externalId"] = *u.ExternalID
	}
	return out
}

func (s *Server) loadSCIMUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*scimUser, error) {
	u, err := scanSCIMUser(tx.QueryRow(ctx, `SELECT `+scimUserCols+` FROM scim_users WHERE user_id = $1`, id))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT g.id, g.display_name FROM scim_group_members m JOIN scim_groups g ON g.id = m.group_id WHERE m.user_id = $1 ORDER BY g.display_name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g scimRef
		if err := rows.Scan(&g.Value, &g.Display); err != nil {
			return nil, err
		}
		g.Ref = s.scimLocation("Groups", g.Value)
		u.groups = append(u.groups, g)
	}
	return u, rows.Err()
}

func (s *Server) scimListUsers(w http.ResponseWriter, r *http.Request) {
	col, val, err := scimFilter(r, map[string]string{"userName": "lower(user_name) = lower($1)", "externalId": "external_id = $1",
		"emails.value": "lower(email) = lower($1)", "emails": "lower(email) = lower($1)", "id": "user_id::text = $1"})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	start, count := scimPage(r)
	where, args := "true", []any{}
	if col != "" {
		where, args = col, []any{val}
	}
	var total int
	var list []any
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM scim_users WHERE `+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM scim_users WHERE `+where+fmt.Sprintf(` ORDER BY created_at, user_id OFFSET %d LIMIT %d`, start-1, count), args...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			u, err := s.loadSCIMUser(ctx, tx, id)
			if err != nil {
				return err
			}
			list = append(list, s.scimUserJSON(u))
		}
		return nil
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	if list == nil {
		list = []any{}
	}
	writeSCIM(w, http.StatusOK, map[string]any{"schemas": []string{scimListSchema}, "totalResults": total, "startIndex": start, "itemsPerPage": len(list), "Resources": list})
}

func (s *Server) scimGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	var u *scimUser
	err = s.tx(r, func(tx pgx.Tx) error {
		u, err = s.loadSCIMUser(r.Context(), tx, id)
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	writeSCIM(w, http.StatusOK, s.scimUserJSON(u))
}

func validSCIMUser(in *scimUserIn) error {
	if strings.TrimSpace(in.UserName) == "" {
		return scimErr(http.StatusBadRequest, "invalidValue", "userName is required")
	}
	if e := in.email(); !strings.Contains(e, "@") || len(e) > 320 {
		return scimErr(http.StatusBadRequest, "invalidValue", "an email is required (emails, or a userName that is an email)")
	}
	return nil
}

func displayName(in *scimUserIn) string {
	if in.DisplayName != "" {
		return in.DisplayName
	}
	if in.Name.Formatted != "" {
		return in.Name.Formatted
	}
	return strings.TrimSpace(in.Name.GivenName + " " + in.Name.FamilyName)
}

func (s *Server) scimCreateUser(w http.ResponseWriter, r *http.Request) {
	var in scimUserIn
	if err := scimDecode(r, &in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	if err := validSCIMUser(&in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	email := strings.ToLower(strings.TrimSpace(in.email()))
	active := in.Active == nil || bool(*in.Active)
	var u *scimUser
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_users WHERE lower(user_name) = lower($1) OR lower(email) = $2)`, in.UserName, email).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return scimErr(http.StatusConflict, "uniqueness", "a user with this userName or email already exists")
		}
		var user uuid.UUID
		invited := false
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, email).Scan(&user)
		if errors.Is(err, pgx.ErrNoRows) {
			user = uuid.Must(uuid.NewV7())
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, user, email, displayName(&in)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if direct, err := joinsDirectly(ctx, tx, user, email); err != nil {
			return err
		} else if !direct {
			// Someone with an account of their own, off the tenant's verified
			// domains: provisioned, but holding nothing until they accept.
			invited = true
			if _, err := tx.Exec(ctx, `INSERT INTO member_invitations (tenant_id, user_id, source, invited_by) VALUES ($1, $2, 'scim', $3)
				ON CONFLICT (tenant_id, user_id) DO UPDATE SET source = 'scim'`, p.TenantID, user, p.Actor()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO scim_users (tenant_id, user_id, user_name, email, external_id, display_name, given_name, family_name, active)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)`,
			p.TenantID, user, in.UserName, email, in.ExternalID, displayName(&in), in.Name.GivenName, in.Name.FamilyName, active); err != nil {
			return err
		}
		ch, err := s.scimSync(ctx, tx, p.TenantID, user)
		if err != nil {
			return err
		}
		if err := auditTx(r, tx, "scim.user.create", user.String(), map[string]any{"user_name": in.UserName, "active": active, "granted": ch.granted, "invited": invited}); err != nil {
			return err
		}
		u, err = s.loadSCIMUser(ctx, tx, user)
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	w.Header().Set("Location", s.scimLocation("Users", u.ID))
	writeSCIM(w, http.StatusCreated, s.scimUserJSON(u))
}

// scimUpdateUser applies a change to one person, then syncs their roles.
func (s *Server) scimUpdateUser(w http.ResponseWriter, r *http.Request, action string, change func(u *scimUser) error) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var u *scimUser
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		u, err = scanSCIMUser(tx.QueryRow(ctx, `SELECT `+scimUserCols+` FROM scim_users WHERE user_id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		was := u.Active
		if err := change(u); err != nil {
			return err
		}
		if strings.TrimSpace(u.UserName) == "" {
			return scimErr(http.StatusBadRequest, "invalidValue", "userName is required")
		}
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_users WHERE lower(user_name) = lower($1) AND user_id <> $2)`, u.UserName, id).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return scimErr(http.StatusConflict, "uniqueness", "userName is taken")
		}
		if _, err := tx.Exec(ctx, `UPDATE scim_users SET user_name = $2, external_id = $3, display_name = $4, given_name = $5, family_name = $6, active = $7, updated_at = now()
			WHERE user_id = $1`, id, u.UserName, u.ExternalID, u.DisplayName, u.GivenName, u.FamilyName, u.Active); err != nil {
			return err
		}
		ch, err := s.scimSync(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		detail := map[string]any{"granted": ch.granted, "revoked": ch.revoked}
		if was != u.Active {
			detail["active"] = u.Active
		}
		if ch.ownerKept {
			detail["owner_kept"] = true
		}
		if err := auditTx(r, tx, action, id.String(), detail); err != nil {
			return err
		}
		u, err = s.loadSCIMUser(ctx, tx, id)
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	writeSCIM(w, http.StatusOK, s.scimUserJSON(u))
}

func (s *Server) scimPutUser(w http.ResponseWriter, r *http.Request) {
	var in scimUserIn
	if err := scimDecode(r, &in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	if err := validSCIMUser(&in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	s.scimUpdateUser(w, r, "scim.user.replace", func(u *scimUser) error {
		// The sign-in email stays as provisioned: it identifies the person
		// across tenants, so one tenant's provider cannot change it.
		u.UserName = in.UserName
		u.ExternalID = nil
		if in.ExternalID != "" {
			u.ExternalID = &in.ExternalID
		}
		u.DisplayName = displayName(&in)
		u.GivenName, u.FamilyName = in.Name.GivenName, in.Name.FamilyName
		u.Active = in.Active == nil || bool(*in.Active)
		return nil
	})
}

type scimPatch struct {
	Operations []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	} `json:"Operations"`
}

func (s *Server) scimPatchUser(w http.ResponseWriter, r *http.Request) {
	var req scimPatch
	if err := scimDecode(r, &req); err != nil {
		s.scimFail(w, r, err)
		return
	}
	s.scimUpdateUser(w, r, "scim.user.patch", func(u *scimUser) error {
		for _, op := range req.Operations {
			kind := strings.ToLower(op.Op)
			if kind != "add" && kind != "replace" && kind != "remove" {
				return scimErr(http.StatusBadRequest, "invalidSyntax", "unknown op %q", op.Op)
			}
			fields := map[string]json.RawMessage{}
			if op.Path != "" {
				fields[op.Path] = op.Value
			} else if err := json.Unmarshal(op.Value, &fields); err != nil {
				return scimErr(http.StatusBadRequest, "invalidValue", "a patch without a path needs an object value")
			}
			for path, raw := range fields {
				if err := patchUserField(u, kind, path, raw); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// patchUserField applies one attribute. Attributes Taskiem does not keep
// (phone numbers, titles, enterprise extension) are accepted and ignored.
func patchUserField(u *scimUser, op, path string, raw json.RawMessage) error {
	str := func() (string, error) {
		if op == "remove" {
			return "", nil
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", scimErr(http.StatusBadRequest, "invalidValue", "%s must be a string", path)
		}
		return v, nil
	}
	var err error
	switch strings.ToLower(path) {
	case "active":
		if op == "remove" {
			return scimErr(http.StatusBadRequest, "mutability", "active cannot be removed")
		}
		var b flexBool
		if err := json.Unmarshal(raw, &b); err != nil {
			return scimErr(http.StatusBadRequest, "invalidValue", "active must be a boolean")
		}
		u.Active = bool(b)
	case "username":
		u.UserName, err = str()
	case "displayname":
		u.DisplayName, err = str()
	case "externalid":
		var v string
		v, err = str()
		u.ExternalID = nil
		if v != "" {
			u.ExternalID = &v
		}
	case "name.givenname":
		u.GivenName, err = str()
	case "name.familyname":
		u.FamilyName, err = str()
	case "name":
		var n struct{ GivenName, FamilyName *string }
		if op != "remove" {
			if err := json.Unmarshal(raw, &n); err != nil {
				return scimErr(http.StatusBadRequest, "invalidValue", "name must be an object")
			}
		}
		if n.GivenName != nil {
			u.GivenName = *n.GivenName
		}
		if n.FamilyName != nil {
			u.FamilyName = *n.FamilyName
		}
	}
	return err
}

// scimDeleteUser deprovisions someone and forgets them.
func (s *Server) scimDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		tag, err := tx.Exec(ctx, `UPDATE scim_users SET active = false WHERE user_id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		ch, err := s.scimSync(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM scim_users WHERE user_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM member_invitations WHERE user_id = $1 AND source = 'scim'`, id); err != nil {
			return err
		}
		return auditTx(r, tx, "scim.user.delete", id.String(), map[string]any{"revoked": ch.revoked, "owner_kept": ch.ownerKept})
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- role sync ---

type scimChange struct {
	granted, revoked []string
	ownerKept        bool
}

type scimMapping struct {
	DefaultRoles []string
	GroupRoles   map[string][]string
}

func loadSCIMMapping(ctx context.Context, tx pgx.Tx) (scimMapping, error) {
	m := scimMapping{DefaultRoles: []string{"viewer"}, GroupRoles: map[string][]string{}}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT default_roles, group_roles FROM scim_config`).Scan(&m.DefaultRoles, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m.GroupRoles)
}

func (m scimMapping) rolesFor(group string) []string {
	for g, roles := range m.GroupRoles {
		if strings.EqualFold(g, group) {
			return roles
		}
	}
	return nil
}

// scimSync brings one person's memberships in line with SCIM: active, they
// hold the default roles and their groups' roles (as SCIM memberships,
// leaving roles granted otherwise); inactive or forgotten, they hold
// nothing and are signed out. An owner keeps everything: owners are
// removed by owners.
func (s *Server) scimSync(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (scimChange, error) {
	var ch scimChange
	m, err := loadSCIMMapping(ctx, tx)
	if err != nil {
		return ch, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM scim_users WHERE user_id = $1`, user).Scan(&active)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ch, err
	}
	// Someone invited, not yet accepted, holds nothing whatever SCIM says;
	// accepting syncs them.
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM member_invitations WHERE user_id = $1 AND source = 'scim')`, user).Scan(&pending); err != nil {
		return ch, err
	}
	if pending {
		return ch, nil
	}
	var want []string
	if active {
		want = slices.Clone(m.DefaultRoles)
		rows, err := tx.Query(ctx, `SELECT g.display_name FROM scim_group_members gm JOIN scim_groups g ON g.id = gm.group_id WHERE gm.user_id = $1`, user)
		if err != nil {
			return ch, err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return ch, err
		}
		for _, g := range groups {
			want = append(want, m.rolesFor(g)...)
		}
		want = slices.DeleteFunc(want, func(r string) bool { return r == "owner" })
		slices.Sort(want)
		want = slices.Compact(want)
	}
	rows, err := tx.Query(ctx, `SELECT role, source FROM memberships WHERE user_id = $1`, user)
	if err != nil {
		return ch, err
	}
	have := map[string]string{}
	for rows.Next() {
		var role, src string
		if err := rows.Scan(&role, &src); err != nil {
			rows.Close()
			return ch, err
		}
		have[role] = src
	}
	rows.Close()
	if !active && have["owner"] != "" {
		ch.ownerKept = true
		return ch, nil
	}
	for _, role := range want {
		if _, ok := have[role]; !ok {
			if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, user_id, role, source) VALUES ($1, $2, $3, 'scim')`, tenant, user, role); err != nil {
				return ch, err
			}
			ch.granted = append(ch.granted, role)
		}
	}
	for role, src := range have {
		if slices.Contains(want, role) || (active && src != "scim") {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE user_id = $1 AND role = $2`, user, role); err != nil {
			return ch, err
		}
		ch.revoked = append(ch.revoked, role)
	}
	slices.Sort(ch.revoked)
	if !active || len(ch.revoked) > 0 {
		// Signed out when nothing is left; narrowed roles apply at the next
		// request anyway.
		if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM memberships WHERE user_id = $1 AND tenant_id = $2)`, user, tenant); err != nil {
			return ch, err
		}
	}
	return ch, nil
}

func (s *Server) scimSyncAll(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID) error {
	slices.SortFunc(users, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, u := range slices.Compact(users) {
		if _, err := s.scimSync(ctx, tx, tenant, u); err != nil {
			return err
		}
	}
	return nil
}

// --- groups ---

type scimGroupIn struct {
	DisplayName string `json:"displayName"`
	ExternalID  string `json:"externalId"`
	Members     []struct {
		Value string `json:"value"`
	} `json:"members"`
}

func (s *Server) scimGroupJSON(ctx context.Context, tx pgx.Tx, id uuid.UUID, withMembers bool) (map[string]any, error) {
	var name string
	var ext *string
	var created, modified time.Time
	if err := tx.QueryRow(ctx, `SELECT display_name, external_id, created_at, updated_at FROM scim_groups WHERE id = $1`, id).Scan(&name, &ext, &created, &modified); err != nil {
		return nil, err
	}
	out := map[string]any{
		"schemas": []string{scimGroupSchema}, "id": id, "displayName": name,
		"meta": map[string]any{"resourceType": "Group", "created": created, "lastModified": modified, "location": s.scimLocation("Groups", id)},
	}
	if ext != nil {
		out["externalId"] = *ext
	}
	if withMembers {
		rows, err := tx.Query(ctx, `SELECT u.user_id, u.user_name FROM scim_group_members m JOIN scim_users u ON u.user_id = m.user_id WHERE m.group_id = $1 ORDER BY u.user_name`, id)
		if err != nil {
			return nil, err
		}
		members := []scimRef{}
		for rows.Next() {
			var m scimRef
			if err := rows.Scan(&m.Value, &m.Display); err != nil {
				rows.Close()
				return nil, err
			}
			m.Ref = s.scimLocation("Users", m.Value)
			members = append(members, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out["members"] = members
	}
	return out, nil
}

func wantMembers(r *http.Request) bool {
	return !strings.Contains(strings.ToLower(r.URL.Query().Get("excludedAttributes")), "members")
}

func (s *Server) scimListGroups(w http.ResponseWriter, r *http.Request) {
	col, val, err := scimFilter(r, map[string]string{"displayName": "lower(display_name) = lower($1)", "externalId": "external_id = $1", "id": "id::text = $1"})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	start, count := scimPage(r)
	where, args := "true", []any{}
	if col != "" {
		where, args = col, []any{val}
	}
	var total int
	list := []any{}
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM scim_groups WHERE `+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM scim_groups WHERE `+where+fmt.Sprintf(` ORDER BY created_at, id OFFSET %d LIMIT %d`, start-1, count), args...)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, id := range ids {
			g, err := s.scimGroupJSON(ctx, tx, id, wantMembers(r))
			if err != nil {
				return err
			}
			list = append(list, g)
		}
		return nil
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	writeSCIM(w, http.StatusOK, map[string]any{"schemas": []string{scimListSchema}, "totalResults": total, "startIndex": start, "itemsPerPage": len(list), "Resources": list})
}

func (s *Server) scimGetGroup(w http.ResponseWriter, r *http.Request) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	var out map[string]any
	err = s.tx(r, func(tx pgx.Tx) error {
		out, err = s.scimGroupJSON(r.Context(), tx, id, wantMembers(r))
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	writeSCIM(w, http.StatusOK, out)
}

// memberIDs reads member references, all of which must be provisioned users.
func memberIDs(ctx context.Context, tx pgx.Tx, values []string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, v := range values {
		id, err := uuid.Parse(v)
		var ok bool
		if err == nil {
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_users WHERE user_id = $1)`, id).Scan(&ok)
			if err != nil {
				return nil, err
			}
		}
		if !ok {
			return nil, scimErr(http.StatusBadRequest, "invalidValue", "member %q is not a provisioned user", v)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Server) scimCreateGroup(w http.ResponseWriter, r *http.Request) {
	var in scimGroupIn
	if err := scimDecode(r, &in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	if strings.TrimSpace(in.DisplayName) == "" {
		s.scimFail(w, r, scimErr(http.StatusBadRequest, "invalidValue", "displayName is required"))
		return
	}
	p := principalFrom(r.Context())
	id := uuid.Must(uuid.NewV7())
	var out map[string]any
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_groups WHERE lower(display_name) = lower($1))`, in.DisplayName).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return scimErr(http.StatusConflict, "uniqueness", "a group with this displayName already exists")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO scim_groups (id, tenant_id, display_name, external_id) VALUES ($1, $2, $3, NULLIF($4, ''))`, id, p.TenantID, in.DisplayName, in.ExternalID); err != nil {
			return err
		}
		vals := make([]string, 0, len(in.Members))
		for _, m := range in.Members {
			vals = append(vals, m.Value)
		}
		added, err := s.setGroupMembers(ctx, tx, p.TenantID, id, vals, "add")
		if err != nil {
			return err
		}
		if err := auditTx(r, tx, "scim.group.create", id.String(), map[string]any{"display_name": in.DisplayName, "members": len(added)}); err != nil {
			return err
		}
		out, err = s.scimGroupJSON(ctx, tx, id, true)
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	w.Header().Set("Location", s.scimLocation("Groups", id))
	writeSCIM(w, http.StatusCreated, out)
}

// setGroupMembers adds, removes or replaces members and syncs everyone
// affected. It returns who was affected.
func (s *Server) setGroupMembers(ctx context.Context, tx pgx.Tx, tenant, group uuid.UUID, values []string, op string) ([]uuid.UUID, error) {
	var affected []uuid.UUID
	if op == "replace" {
		rows, err := tx.Query(ctx, `DELETE FROM scim_group_members WHERE group_id = $1 RETURNING user_id`, group)
		if err != nil {
			return nil, err
		}
		affected, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return nil, err
		}
		op = "add"
	}
	ids, err := memberIDs(ctx, tx, values)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		q := `INSERT INTO scim_group_members (tenant_id, group_id, user_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`
		args := []any{tenant, group, id}
		if op == "remove" {
			q, args = `DELETE FROM scim_group_members WHERE group_id = $1 AND user_id = $2`, []any{group, id}
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return nil, err
		}
		affected = append(affected, id)
	}
	return affected, s.scimSyncAll(ctx, tx, tenant, affected)
}

func groupMembers(ctx context.Context, tx pgx.Tx, group uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM scim_group_members WHERE group_id = $1`, group)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// scimUpdateGroup locks a group, applies a change, and answers with it.
func (s *Server) scimUpdateGroup(w http.ResponseWriter, r *http.Request, action string, change func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (map[string]any, error)) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out map[string]any
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := tx.QueryRow(ctx, `SELECT id FROM scim_groups WHERE id = $1 FOR UPDATE`, id).Scan(&id); err != nil {
			return err
		}
		detail, err := change(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE scim_groups SET updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		if err := auditTx(r, tx, action, id.String(), detail); err != nil {
			return err
		}
		out, err = s.scimGroupJSON(ctx, tx, id, wantMembers(r))
		return err
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	writeSCIM(w, http.StatusOK, out)
}

// renameGroup changes a group's name, which can change its roles.
func (s *Server) renameGroup(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, name string) error {
	if strings.TrimSpace(name) == "" {
		return scimErr(http.StatusBadRequest, "invalidValue", "displayName is required")
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_groups WHERE lower(display_name) = lower($1) AND id <> $2)`, name, id).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return scimErr(http.StatusConflict, "uniqueness", "a group with this displayName already exists")
	}
	if _, err := tx.Exec(ctx, `UPDATE scim_groups SET display_name = $2 WHERE id = $1`, id, name); err != nil {
		return err
	}
	members, err := groupMembers(ctx, tx, id)
	if err != nil {
		return err
	}
	return s.scimSyncAll(ctx, tx, tenant, members)
}

func (s *Server) scimPutGroup(w http.ResponseWriter, r *http.Request) {
	var in scimGroupIn
	if err := scimDecode(r, &in); err != nil {
		s.scimFail(w, r, err)
		return
	}
	s.scimUpdateGroup(w, r, "scim.group.replace", func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (map[string]any, error) {
		if err := s.renameGroup(ctx, tx, tenant, id, in.DisplayName); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE scim_groups SET external_id = NULLIF($2, '') WHERE id = $1`, id, in.ExternalID); err != nil {
			return nil, err
		}
		vals := make([]string, 0, len(in.Members))
		for _, m := range in.Members {
			vals = append(vals, m.Value)
		}
		if _, err := s.setGroupMembers(ctx, tx, tenant, id, vals, "replace"); err != nil {
			return nil, err
		}
		return map[string]any{"display_name": in.DisplayName, "members": len(vals)}, nil
	})
}

// memberPathRe matches members[value eq "id"], how providers remove one.
var memberPathRe = regexp.MustCompile(`^(?i:members)\[\s*(?i:value)\s+(?i:eq)\s+"([^"]+)"\s*\]$`)

func (s *Server) scimPatchGroup(w http.ResponseWriter, r *http.Request) {
	var req scimPatch
	if err := scimDecode(r, &req); err != nil {
		s.scimFail(w, r, err)
		return
	}
	s.scimUpdateGroup(w, r, "scim.group.patch", func(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (map[string]any, error) {
		added, removed := 0, 0
		for _, op := range req.Operations {
			kind := strings.ToLower(op.Op)
			if kind != "add" && kind != "replace" && kind != "remove" {
				return nil, scimErr(http.StatusBadRequest, "invalidSyntax", "unknown op %q", op.Op)
			}
			var members []struct {
				Value string `json:"value"`
			}
			path := strings.ToLower(op.Path)
			switch {
			case path == "members":
				if len(op.Value) > 0 {
					if err := json.Unmarshal(op.Value, &members); err != nil {
						return nil, scimErr(http.StatusBadRequest, "invalidValue", "members must be a list")
					}
				}
				if kind == "remove" && len(members) == 0 {
					kind = "replace" // remove them all
				}
			case memberPathRe.MatchString(op.Path):
				if kind != "remove" {
					return nil, scimErr(http.StatusBadRequest, "invalidPath", "only remove takes a member filter")
				}
				members = append(members, struct {
					Value string `json:"value"`
				}{memberPathRe.FindStringSubmatch(op.Path)[1]})
			case path == "displayname" || path == "":
				var name string
				if path == "" {
					var v struct {
						DisplayName *string `json:"displayName"`
						ExternalID  *string `json:"externalId"`
					}
					if err := json.Unmarshal(op.Value, &v); err != nil {
						return nil, scimErr(http.StatusBadRequest, "invalidValue", "a patch without a path needs an object value")
					}
					if v.ExternalID != nil {
						if _, err := tx.Exec(ctx, `UPDATE scim_groups SET external_id = NULLIF($2, '') WHERE id = $1`, id, *v.ExternalID); err != nil {
							return nil, err
						}
					}
					if v.DisplayName == nil {
						continue
					}
					name = *v.DisplayName
				} else if err := json.Unmarshal(op.Value, &name); err != nil {
					return nil, scimErr(http.StatusBadRequest, "invalidValue", "displayName must be a string")
				}
				if err := s.renameGroup(ctx, tx, tenant, id, name); err != nil {
					return nil, err
				}
				continue
			case path == "externalid":
				var v string
				if kind != "remove" {
					if err := json.Unmarshal(op.Value, &v); err != nil {
						return nil, scimErr(http.StatusBadRequest, "invalidValue", "externalId must be a string")
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE scim_groups SET external_id = NULLIF($2, '') WHERE id = $1`, id, v); err != nil {
					return nil, err
				}
				continue
			default:
				return nil, scimErr(http.StatusBadRequest, "invalidPath", "cannot patch %s", op.Path)
			}
			vals := make([]string, 0, len(members))
			for _, m := range members {
				vals = append(vals, m.Value)
			}
			if kind == "remove" {
				// Removing someone no longer here is not an error.
				vals = slices.DeleteFunc(vals, func(v string) bool {
					uid, err := uuid.Parse(v)
					if err != nil {
						return true
					}
					var ok bool
					_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scim_users WHERE user_id = $1)`, uid).Scan(&ok)
					return !ok
				})
			}
			n, err := s.setGroupMembers(ctx, tx, tenant, id, vals, kind)
			if err != nil {
				return nil, err
			}
			if kind == "remove" {
				removed += len(n)
			} else {
				added += len(vals)
			}
		}
		return map[string]any{"added": added, "removed": removed}, nil
	})
}

func (s *Server) scimDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := scimID(r)
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err = s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		members, err := groupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM scim_groups WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if err := s.scimSyncAll(ctx, tx, p.TenantID, members); err != nil {
			return err
		}
		return auditTx(r, tx, "scim.group.delete", id.String(), map[string]any{"members": len(members)})
	})
	if err != nil {
		s.scimFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- administration (/v1/scim) ---

func (s *Server) getSCIM(w http.ResponseWriter, r *http.Request) {
	type group struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"display_name"`
		Members     int       `json:"members"`
		Roles       []string  `json:"roles"`
	}
	var m scimMapping
	groups := []group{}
	var active, inactive int
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		var err error
		if m, err = loadSCIMMapping(ctx, tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE active), count(*) FILTER (WHERE NOT active) FROM scim_users`).Scan(&active, &inactive); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT g.id, g.display_name, (SELECT count(*) FROM scim_group_members m WHERE m.group_id = g.id) FROM scim_groups g ORDER BY lower(g.display_name)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g group
			if err := rows.Scan(&g.ID, &g.DisplayName, &g.Members); err != nil {
				return err
			}
			g.Roles = m.rolesFor(g.DisplayName)
			if g.Roles == nil {
				g.Roles = []string{}
			}
			groups = append(groups, g)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoint":      strings.TrimRight(s.PublicURL, "/") + "/scim/v2",
		"default_roles": m.DefaultRoles,
		"group_roles":   m.GroupRoles,
		"groups":        groups,
		"users":         map[string]int{"active": active, "inactive": inactive},
	})
}

// putSCIM sets the roles provisioned people receive, then brings everyone
// provisioned in line.
func (s *Server) putSCIM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DefaultRoles []string            `json:"default_roles"`
		GroupRoles   map[string][]string `json:"group_roles"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.DefaultRoles == nil {
		req.DefaultRoles = []string{}
	}
	if req.GroupRoles == nil {
		req.GroupRoles = map[string][]string{}
	}
	all := slices.Clone(req.DefaultRoles)
	for _, roles := range req.GroupRoles {
		all = append(all, roles...)
	}
	for _, role := range all {
		if role == "owner" {
			s.fail(w, r, fmt.Errorf("%w: owner is granted by hand, not provisioned", errBadRequest))
			return
		}
		if !roleNameRe.MatchString(role) {
			s.fail(w, r, fmt.Errorf("%w: bad role name %q", errBadRequest, role))
			return
		}
	}
	p := principalFrom(r.Context())
	raw, _ := json.Marshal(req.GroupRoles)
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := canGrant(ctx, tx, p, all); err != nil {
			return err
		}
		before, err := loadSCIMMapping(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO scim_config (tenant_id, default_roles, group_roles, updated_by) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id) DO UPDATE SET default_roles = $2, group_roles = $3, updated_by = $4, updated_at = now()`,
			p.TenantID, req.DefaultRoles, raw, p.Actor()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id FROM scim_users`)
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		if err := s.scimSyncAll(ctx, tx, p.TenantID, users); err != nil {
			return err
		}
		return auditTx(r, tx, "scim.config", "scim", map[string]any{"default_roles": req.DefaultRoles, "group_roles": req.GroupRoles,
			"before": map[string]any{"default_roles": before.DefaultRoles, "group_roles": before.GroupRoles}})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getSCIM(w, r)
}

// scimDeactivated reports whether a tenant's provider has deactivated
// someone; single sign-on must not let them back in.
func scimDeactivated(ctx context.Context, tx pgx.Tx, user uuid.UUID) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT active FROM scim_users WHERE user_id = $1`, user).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return !active, err
}
