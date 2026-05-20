package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/winshare/zeroops/internal/shared/dto"
)

const (
	// uploadFieldName is the multipart form field name expected by the server
	// (T8 handler at internal/server/uploads.go). MUST match server-side.
	uploadFieldName = "archive"
	// uploadFileName is the suggested filename in the multipart Content-Disposition
	// header. Server ignores it but a tool like curl/postman shows it.
	uploadFileName = "source.tar.zst"
)

// UploadsClient posts a local directory as a tar.zst to the 0ops uploads endpoint.
// Production wiring is done by T18; T17 only owns the HTTP plumbing.
type UploadsClient struct {
	BaseURL     string
	BearerToken string
	HTTP        *http.Client // nil → http.DefaultClient
}

// NewUploadsClient constructs a client with default HTTP.
func NewUploadsClient(baseURL, bearerToken string) *UploadsClient {
	return &UploadsClient{
		BaseURL:     baseURL,
		BearerToken: bearerToken,
	}
}

// UploadError is returned for non-2xx HTTP responses. It carries the parsed
// apperror code+message; HTTPStatus is the raw status code.
type UploadError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *UploadError) Error() string {
	return fmt.Sprintf("upload failed: HTTP %d %s: %s", e.HTTPStatus, e.Code, e.Message)
}

// UploadDir packs srcPath as tar.zst and uploads it to:
//
//	POST {BaseURL}/v1/teams/{teamSlug}/uploads
//
// Content-Type: multipart/form-data with a single "archive" file part.
//
// The pack runs in a goroutine writing to an io.Pipe; the main routine
// streams the pipe through multipart into the HTTP body. No full buffer.
//
// Error priority chain on failure:
//  1. ctx.Err() — caller cancellation always wins
//  2. server 4xx/5xx — if the server rejected the request, its parsed
//     *UploadError is preferred over any mid-stream pack write error
//     (the server response is the cause; the broken-pipe write is the symptom)
//  3. pack error (e.g., ErrTarballTooLarge mid-stream) — when no server
//     response is available
//  4. HTTP transport error (unreachable, TLS, etc.)
//
// The supplied context should carry a deadline; UploadDir does not set its own
// HTTP timeout. For a CLI use with no explicit --timeout flag, prefer
// context.WithTimeout(parent, 10 * time.Minute) or similar. Without a deadline,
// a stalled connection blocks the call indefinitely.
//
// On 201 Created, returns the server's UploadResponse plus the client-computed
// PackResult.
func (c *UploadsClient) UploadDir(ctx context.Context, teamSlug, srcPath string, opt PackOptions) (dto.UploadResponse, PackResult, error) {
	if c.BaseURL == "" {
		return dto.UploadResponse{}, PackResult{}, errors.New("cli: BaseURL is required")
	}
	if c.BearerToken == "" {
		return dto.UploadResponse{}, PackResult{}, errors.New("cli: BearerToken is required")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	contentType := mw.FormDataContentType()

	// packErr captures the goroutine's failure. Read-after-Done in main.
	var (
		packErr    error
		packResult PackResult
	)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer func() {
			// Always close pw, even on PackDir error, so the http.Do reads EOF
			// (with the error propagated via packErr).
			if packErr != nil {
				_ = pw.CloseWithError(packErr)
			} else {
				_ = pw.Close()
			}
		}()
		defer mw.Close()

		part, err := mw.CreateFormFile(uploadFieldName, uploadFileName)
		if err != nil {
			packErr = fmt.Errorf("multipart CreateFormFile: %w", err)
			return
		}
		res, err := PackDir(srcPath, part, opt)
		if err != nil {
			packErr = err
			return
		}
		packResult = res
	}()

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/teams/" + url.PathEscape(teamSlug) + "/uploads"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = pr.Close()
		<-done
		return dto.UploadResponse{}, PackResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.BearerToken)

	resp, doErr := httpClient.Do(req)

	// Wait for the goroutine before deciding which error to surface.
	<-done

	// 1. ctx cancellation has highest priority (preserves errors.Is(err, context.Canceled))
	if ctxErr := ctx.Err(); ctxErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return dto.UploadResponse{}, PackResult{}, ctxErr
	}

	// 2. If we have a server response with non-2xx status, that's the actionable
	//    cause — even if packErr was set as a symptom of the server closing the
	//    body reader mid-stream. Surface the server's error.
	if resp != nil && resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return dto.UploadResponse{}, PackResult{}, decodeUploadError(resp)
	}

	// 3. Pack error (e.g., MaxBytes cap hit, file unreadable) without server
	//    response — the root cause.
	if packErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return dto.UploadResponse{}, PackResult{}, packErr
	}

	// 4. HTTP transport error (server unreachable, TLS, etc.)
	if doErr != nil {
		return dto.UploadResponse{}, PackResult{}, fmt.Errorf("http do: %w", doErr)
	}

	defer resp.Body.Close()

	// 5. 2xx but unexpected status would also fall here; only 201 is expected.
	if resp.StatusCode != http.StatusCreated {
		return dto.UploadResponse{}, PackResult{}, decodeUploadError(resp)
	}

	var out dto.UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return dto.UploadResponse{}, PackResult{}, fmt.Errorf("decode response: %w", err)
	}
	return out, packResult, nil
}

// decodeUploadError reads the apperror envelope from a non-2xx response body.
func decodeUploadError(resp *http.Response) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// LimitReader caps memory consumption; json.Decoder stops at the end of
	// the first JSON value so trailing bytes (e.g., logs/garbage) don't break
	// parsing.
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&env)
	return &UploadError{HTTPStatus: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
}
