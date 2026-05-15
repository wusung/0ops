package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// QueryScope captures the RBAC requirement applied to a Query call:
// "team" means the caller is admin+ and may see every row; "self"
// restricts results to rows whose actor_user_id equals the caller.
type QueryScope string

const (
	// QueryScopeTeam grants visibility across the whole team.
	QueryScopeTeam QueryScope = "team"
	// QueryScopeSelf restricts visibility to the caller's own rows.
	QueryScopeSelf QueryScope = "self"
)

// QueryFilter captures the wire-level filter parameters from the HTTP
// router or CLI. Empty fields are treated as wildcards. Action is a
// prefix match so callers can filter on action families
// (e.g. "delete_" matches every delete_* action; spec § 6.1).
type QueryFilter struct {
	TeamID      string
	Since       time.Time
	Until       time.Time
	Action      string
	ActorUserID string
	TraceID     string
	PageSize    int
	Cursor      string
	Scope       QueryScope
}

// QueryResult is the typed shape returned from Reader.Query.
type QueryResult struct {
	Items      []Row
	NextCursor string
}

// Row mirrors the columns exposed via the HTTP / MCP query API. Args
// and result are the post-redaction JSON payloads stored at write time;
// the reader does not redact a second time.
type Row struct {
	ID          int64
	TeamID      string
	ActorUserID *string
	ActorLogin  *string
	Source      string
	SubjectType string
	SubjectID   *string
	Action      string
	Args        json.RawMessage
	Result      json.RawMessage
	PreviewID   *string
	TraceID     string
	Outcome     string
	HTTPStatus  *int
	CreatedAt   time.Time
}

// Reader is the persistence boundary used by Service.Query.
type Reader interface {
	ListAuditLog(ctx context.Context, filter QueryFilter) (QueryResult, error)
	GetAuditLogByID(ctx context.Context, teamID string, id int64) (Row, error)
}

// MaxPageSize bounds page_size at the spec § 6.1 ceiling so a caller
// cannot tar-pit the server with an unbounded scan.
const MaxPageSize = 200

// DefaultPageSize is the spec § 6.1 default page size.
const DefaultPageSize = 50

// DefaultSinceWindow controls the implicit `since` value when the
// caller did not pass one (spec § 6.1).
const DefaultSinceWindow = 7 * 24 * time.Hour

// Query lists audit_log rows under the supplied filter. The service
// normalises the filter (page_size / since defaults), honours the
// scope-based RBAC contract, and forwards the call to the reader.
func (s *Service) Query(ctx context.Context, filter QueryFilter) (QueryResult, error) {
	if s == nil {
		return QueryResult{}, errors.New("audit: nil service")
	}
	normalised, err := normaliseFilter(filter)
	if err != nil {
		s.observer.ObserveQuery(string(filter.Scope), "validation_error")
		return QueryResult{}, err
	}
	out, err := s.reader.ListAuditLog(ctx, normalised)
	if err != nil {
		s.observer.ObserveQuery(string(filter.Scope), "read_error")
		return QueryResult{}, fmt.Errorf("audit: list: %w", err)
	}
	s.observer.ObserveQuery(string(filter.Scope), "success")
	return out, nil
}

// Get returns a single audit_log row by id. team scoping is enforced
// by the reader; an actor-restricted lookup compares actor_user_id
// against the supplied actorUserID and returns ErrForbidden when the
// row is owned by somebody else.
func (s *Service) Get(ctx context.Context, teamID string, id int64, scope QueryScope, actorUserID string) (Row, error) {
	if s == nil {
		return Row{}, errors.New("audit: nil service")
	}
	row, err := s.reader.GetAuditLogByID(ctx, teamID, id)
	if err != nil {
		s.observer.ObserveQuery(string(scope), "read_error")
		return Row{}, err
	}
	if scope == QueryScopeSelf {
		if row.ActorUserID == nil || *row.ActorUserID != actorUserID {
			s.observer.ObserveQuery(string(scope), "forbidden")
			return Row{}, ErrForbidden
		}
	}
	s.observer.ObserveQuery(string(scope), "success")
	return row, nil
}

// ErrForbidden is returned by Get when the requested row is outside
// the caller's RBAC scope (spec § 6.2).
var ErrForbidden = errors.New("audit: forbidden")

// ErrNotFound is returned by the reader when no row matches.
var ErrNotFound = errors.New("audit: not found")

// EncodeCursor opaquely serialises the (created_at, id) tuple used by
// the keyset pagination scheme. The cursor is base64-encoded JSON so
// it can survive HTTP query-string transport without escaping.
func EncodeCursor(createdAt time.Time, id int64) string {
	raw, err := json.Marshal(cursorPayload{CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an EncodeCursor output back into its components.
func DecodeCursor(raw string) (time.Time, int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("audit: invalid cursor: %w", err)
	}
	var payload cursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return time.Time{}, 0, fmt.Errorf("audit: invalid cursor payload: %w", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("audit: invalid cursor timestamp: %w", err)
	}
	return ts, payload.ID, nil
}

type cursorPayload struct {
	CreatedAt string `json:"t"`
	ID        int64  `json:"i"`
}

// ParsePageSize parses a raw query-string value into a clamped page
// size; missing input returns DefaultPageSize. Invalid input returns
// an error so the HTTP layer can surface a 400 to the caller.
func ParsePageSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("audit: invalid page_size %q", raw)
	}
	if n > MaxPageSize {
		return 0, fmt.Errorf("audit: page_size exceeds %d", MaxPageSize)
	}
	return n, nil
}

func normaliseFilter(in QueryFilter) (QueryFilter, error) {
	out := in
	if strings.TrimSpace(out.TeamID) == "" {
		return QueryFilter{}, errors.New("audit: team_id is required")
	}
	if out.Scope == "" {
		out.Scope = QueryScopeTeam
	}
	if out.Scope != QueryScopeTeam && out.Scope != QueryScopeSelf {
		return QueryFilter{}, fmt.Errorf("audit: invalid scope %q", out.Scope)
	}
	if out.Scope == QueryScopeSelf {
		if strings.TrimSpace(out.ActorUserID) == "" {
			return QueryFilter{}, errors.New("audit: self scope requires actor_user_id")
		}
	}
	if out.PageSize <= 0 {
		out.PageSize = DefaultPageSize
	}
	if out.PageSize > MaxPageSize {
		out.PageSize = MaxPageSize
	}
	now := time.Now().UTC()
	if out.Until.IsZero() {
		out.Until = now
	}
	if out.Since.IsZero() {
		out.Since = out.Until.Add(-DefaultSinceWindow)
	}
	if out.Since.After(out.Until) {
		return QueryFilter{}, errors.New("audit: since must be <= until")
	}
	return out, nil
}
