package server

import (
	crand "crypto/rand"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// uploadsStore is the persistence boundary the upload handler needs.
// Production is *db.Repository (T5); tests substitute the fakeStore.
type uploadsStore interface {
	InsertUpload(ctx context.Context, u db.Upload) error
}

// ingestionStore is the on-disk boundary for upload streaming.
// Production is *ingestion.Store (T6); tests substitute an in-memory fake.
type ingestionStore interface {
	Put(ctx context.Context, teamID, uploadID string, r io.Reader, format string) (ingestion.Stored, error)
}

// uploadAuditWriter is the audit boundary used by the upload handler.
// nil is explicitly allowed — matches the existing auditSvc nil-tolerant
// pattern in apps.go (see the incidentSvc nil-guard at line 1441).
type uploadAuditWriter interface {
	Log(ctx context.Context, entry audit.Entry) error
}

// uploadInertTTL is the lifetime of an un-pinned upload row.
// Spec §10: inert uploads expire 24h after receipt; pinning at confirm
// time (T13) extends the deadline to deploy_run.terminal_at + 7d.
const uploadInertTTL = 24 * time.Hour

// maxMultipartParts limits the number of multipart parts the upload handler
// will process. The protocol only needs two (sha256 + archive); the cap
// guards against an attacker streaming thousands of unknown parts to exhaust
// goroutine time.
const maxMultipartParts = 10

// errUnrecognizedFormat is the package-local sentinel returned by
// detectArchiveFormat when the leading magic bytes don't match any
// supported archive container. writeIngestError matches via errors.Is.
var errUnrecognizedFormat = errors.New("ingestion: unrecognized archive format")

// detectArchiveFormat sniffs the leading bytes of a stream to choose
// "tar.zst" vs "tar.gz". Returns a non-nil error when magic doesn't match.
func detectArchiveFormat(peek []byte) (string, error) {
	// zstd magic: 0xFD 0x2F 0xB5 0x28 (little-endian frame magic)
	if len(peek) >= 4 && peek[0] == 0x28 && peek[1] == 0xb5 && peek[2] == 0x2f && peek[3] == 0xfd {
		return "tar.zst", nil
	}
	// gzip magic: 0x1F 0x8B
	if len(peek) >= 2 && peek[0] == 0x1f && peek[1] == 0x8b {
		return "tar.gz", nil
	}
	return "", errUnrecognizedFormat
}

// newUploadID returns a "upl_<24-base64url-chars>" identifier built from
// 18 bytes of crypto/rand entropy. Shape matches newRandomToken in apps.go.
func newUploadID() (string, error) {
	var buf [18]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return "", err
	}
	return "upl_" + base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// uploadHandler returns the POST /v1/teams/{team_slug}/uploads handler.
//
// Auth is enforced at router level via:
//
//	mw.Bearer → mw.ResolveTeam → mw.CheckMembership → mw.CheckTokenScope(ActionCreateUpload)
//
// The handler:
//  1. Reads the multipart body, sniffs format from magic bytes.
//  2. Streams the "archive" part through ingestionStore.Put (sha256 computed there).
//  3. Optionally cross-checks a client-supplied "sha256" field.
//  4. Inserts a row via uploadsStore.InsertUpload with status="received", expires_at=now+24h.
//  5. Audits "app_source.upload.created" on success (slog.Warn on audit failure — response is still 201).
//  6. Returns 201 Created with dto.UploadResponse.
func uploadHandler(uploadStore uploadsStore, ingest ingestionStore, auditSvc uploadAuditWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		teamID := auth.TeamID(ctx)
		actorUserID := auth.ActorUserID(ctx)
		if actorUserID == "" {
			apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "missing actor identity", nil)
			return
		}

		mr, err := r.MultipartReader()
		if err != nil {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "expected multipart/form-data body", nil)
			return
		}

		uploadID, err := newUploadID()
		if err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "id generation failed", nil)
			return
		}

		var clientSHA256 string
		var stored ingestion.Stored
		var format string
		archiveReceived := false
		partCount := 0

		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "malformed multipart payload", nil)
				return
			}
			partCount++
			if partCount > maxMultipartParts {
				_ = part.Close()
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "too many multipart parts", nil)
				return
			}

			name := part.FormName()
			switch name {
			case "sha256":
				v, _ := io.ReadAll(io.LimitReader(part, 256))
				clientSHA256 = strings.TrimSpace(string(v))
				_ = part.Close()

			case "archive":
				if archiveReceived {
					_ = part.Close()
					apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "duplicate archive part", nil)
					return
				}
				stored, format, err = streamArchivePart(ctx, ingest, teamID, uploadID, part)
				_ = part.Close()
				if err != nil {
					writeIngestError(w, err)
					return
				}
				archiveReceived = true

			default:
				// Ignore unknown form fields.
				_ = part.Close()
			}
		}

		if !archiveReceived || stored.SHA256 == "" {
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "archive part is required", map[string]any{"field": "archive"})
			return
		}

		if clientSHA256 != "" && !strings.EqualFold(clientSHA256, stored.SHA256) {
			apperror.Write(w, apperror.CodeSHA256Mismatch, apperror.ClassUnprocessable,
				"client-supplied sha256 differs from server-computed value", nil)
			return
		}

		row := db.Upload{
			ID:            uploadID,
			TeamID:        teamID,
			ActorUserID:   actorUserID,
			SizeBytes:     stored.SizeBytes,
			SHA256:        stored.SHA256,
			ArchiveFormat: format,
			Status:        "received",
			ExpiresAt:     stored.ReceivedAt.Add(uploadInertTTL),
		}
		if err := uploadStore.InsertUpload(ctx, row); err != nil {
			apperror.Write(w, "internal_error", apperror.ClassInternal, "failed to record upload", nil)
			return
		}

		if auditSvc != nil {
			httpStatus := http.StatusCreated
			actor := actorUserID
			subjID := uploadID
			entry := audit.Entry{
				TeamID:      teamID,
				ActorUserID: strPtrIfNonEmpty(actor),
				Source:      audit.SourceUser,
				SubjectType: "upload",
				SubjectID:   &subjID,
				Action:      "app_source.upload.created",
				Args:        nil,
				Result: map[string]any{
					"sha256":     stored.SHA256,
					"size_bytes": stored.SizeBytes,
					"format":     format,
				},
				Outcome:    audit.OutcomeSuccess,
				HTTPStatus: &httpStatus,
			}
			if err := auditSvc.Log(ctx, entry); err != nil {
				slog.Warn("uploads: audit log failed",
					"team", teamID, "upload", uploadID, "err", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(dto.UploadResponse{
			UploadID:   uploadID,
			TeamID:     teamID,
			SizeBytes:  stored.SizeBytes,
			SHA256:     stored.SHA256,
			Format:     format,
			ReceivedAt: stored.ReceivedAt,
			ExpiresAt:  row.ExpiresAt,
		})
	}
}

// streamArchivePart sniffs the archive format from magic bytes, then
// delegates streaming + sha256 computation to ingestionStore.Put.
// Caller is responsible for closing part.
func streamArchivePart(ctx context.Context, ingest ingestionStore, teamID, uploadID string, part *multipart.Part) (ingestion.Stored, string, error) {
	// Peek at the first 4 bytes for magic-byte detection without consuming them.
	var head [4]byte
	n, _ := io.ReadFull(part, head[:])

	format, err := detectArchiveFormat(head[:n])
	if err != nil {
		return ingestion.Stored{}, "", err
	}

	// Re-stitch the peeked bytes with the remainder of the part.
	reader := io.MultiReader(strings.NewReader(string(head[:n])), part)
	stored, err := ingest.Put(ctx, teamID, uploadID, reader, format)
	if err != nil {
		return ingestion.Stored{}, "", err
	}
	return stored, format, nil
}

// writeIngestError maps ingestion error sentinel values to apperror codes
// per spec §14 and §11.
func writeIngestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ingestion.ErrArchiveTooLarge),
		errors.Is(err, ingestion.ErrEntryTooLarge),
		errors.Is(err, ingestion.ErrTooManyEntries):
		apperror.Write(w, apperror.CodePayloadTooLarge, apperror.ClassUnprocessable,
			"archive exceeds configured size limits", nil)
	case errors.Is(err, ingestion.ErrUnsupportedFormat),
		errors.Is(err, errUnrecognizedFormat):
		apperror.Write(w, apperror.CodeUnsupportedArchive, apperror.ClassUnprocessable,
			"archive format is not supported", nil)
	case errors.Is(err, ingestion.ErrPathEscape):
		apperror.Write(w, apperror.CodeArchiveCorrupt, apperror.ClassUnprocessable,
			"archive contains an entry that escapes the tree", nil)
	default:
		apperror.Write(w, apperror.CodeArchiveCorrupt, apperror.ClassUnprocessable,
			"archive is invalid or corrupt", nil)
	}
}

// strPtrIfNonEmpty returns a pointer to s if s is non-empty, else nil.
// Used to convert the string returned by auth.ActorUserID into the *string
// expected by audit.Entry.ActorUserID.
func strPtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
