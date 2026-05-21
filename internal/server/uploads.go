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
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/winshare/zeroops/internal/server/apperror"
	"github.com/winshare/zeroops/internal/server/auth"
	"github.com/winshare/zeroops/internal/server/db"
	"github.com/winshare/zeroops/internal/server/middleware/ratelimit"
	"github.com/winshare/zeroops/internal/server/services/audit"
	"github.com/winshare/zeroops/internal/server/services/createapp/ingestion"
	"github.com/winshare/zeroops/internal/shared/dto"
)

// uploadsStore is the persistence boundary the upload handler needs.
// Production is *db.Repository (T5); tests substitute the fakeStore.
type uploadsStore interface {
	InsertUpload(ctx context.Context, u db.Upload) error
}

// uploadArchiveStore extends uploadsStore with the read methods needed for
// the archive download path. Production = *db.Repository.
type uploadArchiveStore interface {
	uploadsStore
	GetUpload(ctx context.Context, teamID, id string) (db.Upload, error)
}

// ingestionWriter is the on-disk boundary for upload streaming (write path only).
// Production is *ingestion.Store (T6); tests substitute an in-memory fake.
// Named ingestionWriter (formerly ingestionStore) to distinguish from the read path.
type ingestionWriter interface {
	Put(ctx context.Context, teamID, uploadID string, r io.Reader, format string) (ingestion.Stored, error)
}

// ingestionStore is an alias for ingestionWriter retained for backward compatibility
// within existing test helpers that reference this name.
type ingestionStore = ingestionWriter

// archiveReader provides the download read path for the archive bytes.
// Production = *ingestion.Store. Kept narrow: just the methods T9 needs.
type archiveReader interface {
	Archive(ctx context.Context, teamID, uploadID string) (io.ReadCloser, error)
}

// ingestionReadOpener exposes per-file read access on an ingest tree.
// Production: *ingestion.Store (T6).
//
// Why this is separate from archiveReader: archiveReader.Archive returns
// the full archive blob (consumed by T9's GET /v1/uploads/{id}/archive
// handler), while Open returns a single file inside the extracted tree
// (consumed by T10's UploadInspector to read package.json, .git/HEAD etc.).
// They serve different consumers and have different cap semantics; keeping
// them as separate one-method interfaces follows the existing ingestionWriter
// / archiveReader decomposition pattern in this file.
type ingestionReadOpener interface {
	Open(ctx context.Context, teamID, uploadID, relPath string) (io.ReadCloser, error)
}

// ingestionStoreFull embeds write, archive-read, and file-open paths. The
// router constructor takes this so it can feed the same *ingestion.Store to
// the upload handler (PUT path), the archive download handler (GET path), and
// the UploadInspector (Open path, T12).
type ingestionStoreFull interface {
	ingestionWriter
	archiveReader
	ingestionReadOpener
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

// DefaultUploadMaxArchiveBytes is the default upper bound for a single upload
// archive (tar.zst or tar.gz). Used by:
//   - ingestion.Store.MaxArchiveBytes (T6) to abort mid-stream uploads
//   - uploadQuota.checkUploadQuota's reserve-max model (T20) to pre-reserve
//     this many bytes against the team's inert cap
//   - CLI upload-max-bytes default (T18) for client-side validation
//
// Self-hosted operators can override via APP_SOURCE_UPLOAD_MAX_BYTES env;
// CLI users can override via --upload-max-bytes flag.
const DefaultUploadMaxArchiveBytes int64 = 100 * 1024 * 1024 // 100 MiB

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
//  1. Checks per-team upload quotas (T20) before reading the body.
//  2. Reads the multipart body, sniffs format from magic bytes.
//  3. Streams the "archive" part through ingestionStore.Put (sha256 computed there).
//  4. Optionally cross-checks a client-supplied "sha256" field.
//  5. Inserts a row via uploadsStore.InsertUpload with status="received", expires_at=now+24h.
//  6. Audits "app_source.upload.created" on success (slog.Warn on audit failure — response is still 201).
//  7. Returns 201 Created with dto.UploadResponse.
//
// quotaStore and quotaTiers are nil-tolerant: if either is nil, the quota check
// is skipped. Production always provides both; tests can stub nil.
// quotaMaxArchiveBytes is the per-upload size cap used as an upper-bound
// reservation for the inert-bytes quota (default 100 MB).
// nowFn is injected for testing the rolling-window boundary; pass nil to
// default to time.Now.
func uploadHandler(
	uploadStore uploadsStore,
	ingest ingestionStore,
	auditSvc uploadAuditWriter,
	quotaStore uploadQuotaStore,
	quotaTiers map[ratelimit.Plan]UploadQuotaTier,
	quotaMaxArchiveBytes int64,
	nowFn func() time.Time,
) http.HandlerFunc {
	if nowFn == nil {
		nowFn = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		teamID := auth.TeamID(ctx)
		actorUserID := auth.ActorUserID(ctx)
		if actorUserID == "" {
			apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "missing actor identity", nil)
			return
		}

		// Quota check BEFORE reading multipart body — fail fast, save bandwidth.
		if quotaStore != nil && quotaTiers != nil {
			plan := ratelimit.Normalize(string(auth.TeamPlan(ctx)))
			if err := checkUploadQuota(ctx, quotaStore, quotaTiers, teamID, plan, quotaMaxArchiveBytes, nowFn); err != nil {
				if IsQuotaExceeded(err) {
					quotaReason := quotaReasonFromError(err)
					slog.Info("uploads: quota rejected",
						"team", teamID,
						"actor", actorUserID,
						"reason", err.Error(),
					)
					if auditSvc != nil {
						httpStatus := http.StatusUnprocessableEntity
						entry := audit.Entry{
							TeamID:      teamID,
							ActorUserID: strPtrIfNonEmpty(actorUserID),
							Source:      audit.SourceUser,
							SubjectType: "upload",
							SubjectID:   nil, // no upload row was created
							Action:      "app_source.upload.quota_rejected",
							Args:        nil,
							Result: map[string]any{
								"reason":        quotaReason,
								"reason_detail": err.Error(),
							},
							Outcome:    audit.OutcomeFailure,
							HTTPStatus: &httpStatus,
						}
						if logErr := auditSvc.Log(ctx, entry); logErr != nil {
							slog.Warn("uploads: quota audit failed", "err", logErr)
						}
					}
					recordQuotaRejectionMetric(quotaReason)
					apperror.Write(w, apperror.CodeTeamQuotaExceeded, apperror.ClassUnprocessable, err.Error(), nil)
					return
				}
				// DB error during quota check
				apperror.Write(w, "internal_error", apperror.ClassInternal, "quota check failed", nil)
				return
			}
		}

		mr, err := r.MultipartReader()
		if err != nil {
			recordUploadRejectionMetric("malformed_multipart")
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
				recordUploadRejectionMetric("malformed_multipart")
				apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "malformed multipart payload", nil)
				return
			}
			partCount++
			if partCount > maxMultipartParts {
				_ = part.Close()
				recordUploadRejectionMetric("too_many_parts")
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
					recordUploadRejectionMetric("duplicate_archive")
					apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "duplicate archive part", nil)
					return
				}
				stored, format, err = streamArchivePart(ctx, ingest, teamID, uploadID, part)
				_ = part.Close()
				if err != nil {
					recordUploadRejectionMetric(ingestRejectionReason(err))
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
			recordUploadRejectionMetric("missing_archive")
			apperror.Write(w, "validation_failed", apperror.ClassBadRequest, "archive part is required", map[string]any{"field": "archive"})
			return
		}

		if clientSHA256 != "" && !strings.EqualFold(clientSHA256, stored.SHA256) {
			recordUploadRejectionMetric("sha256_mismatch")
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

		recordUploadSuccessMetric(stored.SizeBytes, time.Since(start))
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

// ingestRejectionReason maps ingestion error sentinels to the string used
// as the reject_reason label in app_source_upload_total.
func ingestRejectionReason(err error) string {
	switch {
	case errors.Is(err, ingestion.ErrArchiveTooLarge),
		errors.Is(err, ingestion.ErrEntryTooLarge),
		errors.Is(err, ingestion.ErrTooManyEntries):
		return "payload_too_large"
	case errors.Is(err, ingestion.ErrUnsupportedFormat),
		errors.Is(err, errUnrecognizedFormat):
		return "unsupported_format"
	default:
		return "archive_corrupt"
	}
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

// quotaReasonFromError extracts the short quota dimension from a *quotaError.
// Returns "pinned", "daily", "inert_bytes", or "unknown".
// Uses the compile-time-safe Dimension field instead of string-matching Reason.
func quotaReasonFromError(err error) string {
	var qe *quotaError
	if !errors.As(err, &qe) {
		return "unknown"
	}
	if qe.Dimension != "" {
		return string(qe.Dimension)
	}
	return "unknown"
}

// uploadArchiveHandler returns the GET /v1/uploads/{id}/archive handler.
//
// Authenticated via a short-lived HS256 JWT (scope=download-upload), NOT the
// user bearer token. The handler:
//  1. Verifies the JWT (signer enforces HS256, audience, issuer, scope, expiry, subject↔upload_id consistency).
//  2. Cross-checks the JWT's UploadID against the {id} URL param.
//  3. Looks up the Upload row scoped by JWT.TeamID; treats cross-team as 404.
//  4. Refuses 'expired' / 'gc''d' rows.
//  5. Streams the archive bytes with the recorded ArchiveFormat as Content-Type.
//  6. Audits app_source.upload.archive_downloaded on success.
//
// nil tokenSigner / nil archive store → route is not registered (mirrors T8 nil-guard).
func uploadArchiveHandler(store uploadArchiveStore, archive archiveReader, signer *ingestion.TokenSigner, auditSvc uploadAuditWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		urlID := chi.URLParam(r, "id")

		rawAuth := r.Header.Get("Authorization")
		if !strings.HasPrefix(rawAuth, "Bearer ") {
			recordArchiveDownloadMetric("unauthorized")
			apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "missing bearer token", nil)
			return
		}
		tokenStr := strings.TrimPrefix(rawAuth, "Bearer ")

		claims, err := signer.Verify(tokenStr)
		if err != nil {
			switch {
			case errors.Is(err, ingestion.ErrTokenExpired):
				recordArchiveDownloadMetric("unauthorized")
				apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "token expired", nil)
			case errors.Is(err, ingestion.ErrTokenScopeMismatch):
				recordArchiveDownloadMetric("forbidden")
				apperror.Write(w, "forbidden", apperror.ClassForbidden, "token scope mismatch", nil)
			default:
				recordArchiveDownloadMetric("unauthorized")
				apperror.Write(w, "unauthorized", apperror.ClassUnauthorized, "invalid token", nil)
			}
			return
		}

		if claims.UploadID != urlID {
			recordArchiveDownloadMetric("forbidden")
			apperror.Write(w, "forbidden", apperror.ClassForbidden, "token does not match upload", nil)
			return
		}

		upload, err := store.GetUpload(ctx, claims.TeamID, urlID)
		if err != nil {
			if errors.Is(err, db.ErrUploadNotFound) {
				recordArchiveDownloadMetric("not_found")
				apperror.Write(w, apperror.CodeSourceNotFound, apperror.ClassNotFound, "upload not found", nil)
				return
			}
			recordArchiveDownloadMetric("internal_error")
			apperror.Write(w, "internal_error", apperror.ClassInternal, "lookup failed", nil)
			return
		}

		// Status gate. Allowed: 'received' and 'pinned'. Anything else (expired/gc'd) → 404 source_expired.
		// (apperror.Class has no 410; spec §14 lists "410 source_expired" but we
		//  map to 404 to fit the existing Class taxonomy. T20 may add a 410 Class.)
		switch upload.Status {
		case "received", "pinned":
			// continue
		default:
			recordArchiveDownloadMetric("expired")
			apperror.Write(w, apperror.CodeSourceExpired, apperror.ClassNotFound, "upload no longer available", nil)
			return
		}

		rc, err := archive.Archive(ctx, upload.TeamID, upload.ID)
		if err != nil {
			recordArchiveDownloadMetric("internal_error")
			if auditSvc != nil {
				_ = logArchiveFailedAudit(ctx, auditSvc, upload)
			}
			apperror.Write(w, "internal_error", apperror.ClassInternal, "archive read failed", nil)
			return
		}
		defer rc.Close()

		contentType := "application/zstd"
		if upload.ArchiveFormat == "tar.gz" {
			contentType = "application/gzip"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.FormatInt(upload.SizeBytes, 10))
		w.WriteHeader(http.StatusOK)

		if _, err := io.Copy(w, rc); err != nil {
			// Client disconnect mid-stream is common; do not error-log noisily.
			slog.Debug("uploads: archive stream truncated", "upload", upload.ID, "err", err)
			return
		}

		recordArchiveDownloadMetric("success")
		if auditSvc != nil {
			httpStatus := http.StatusOK
			subjID := upload.ID
			result := map[string]any{
				"size_bytes": upload.SizeBytes,
			}
			if claims.DeployRunID != "" {
				result["deploy_run_id"] = claims.DeployRunID
			}
			entry := audit.Entry{
				TeamID:      upload.TeamID,
				ActorUserID: nil, // workflow has no user actor
				Source:      audit.SourceSystem,
				SubjectType: "upload",
				SubjectID:   &subjID,
				Action:      "app_source.upload.archive_downloaded",
				Args:        nil,
				Result:      result,
				Outcome:     audit.OutcomeSuccess,
				HTTPStatus:  &httpStatus,
			}
			if err := auditSvc.Log(ctx, entry); err != nil {
				slog.Warn("uploads: archive download audit log failed",
					"team", upload.TeamID, "upload", upload.ID, "err", err)
			}
		}
	}
}

// logArchiveFailedAudit writes an audit entry for archive read failures
// (file missing on disk despite DB row existing).
func logArchiveFailedAudit(ctx context.Context, auditSvc uploadAuditWriter, upload db.Upload) error {
	subjID := upload.ID
	httpStatus := http.StatusInternalServerError
	return auditSvc.Log(ctx, audit.Entry{
		TeamID:      upload.TeamID,
		ActorUserID: nil,
		Source:      audit.SourceSystem,
		SubjectType: "upload",
		SubjectID:   &subjID,
		Action:      "app_source.upload.archive_downloaded",
		Args:        nil,
		Result:      nil,
		Outcome:     audit.OutcomeFailure,
		HTTPStatus:  &httpStatus,
	})
}
