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
// Pack errors (including ErrTarballTooLarge mid-stream) propagate to the
// returned error, preferentially over any HTTP transport error.
//
// On 201 Created, returns the server's UploadResponse.
// On non-2xx, attempts to decode the apperror envelope; returns an
// UploadError carrying the parsed code/message.
func (c *UploadsClient) UploadDir(ctx context.Context, teamSlug, srcPath string, opt PackOptions) (dto.UploadResponse, error) {
	if c.BaseURL == "" {
		return dto.UploadResponse{}, errors.New("cli: BaseURL is required")
	}
	if c.BearerToken == "" {
		return dto.UploadResponse{}, errors.New("cli: BearerToken is required")
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

		part, err := mw.CreateFormFile("archive", "source.tar.zst")
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
		return dto.UploadResponse{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.BearerToken)

	resp, doErr := httpClient.Do(req)

	// Wait for the goroutine before deciding which error to surface.
	<-done

	if packErr != nil {
		// Pack error is preferred over HTTP error — it's the root cause.
		// (HTTP error is likely "io: read/write on closed pipe" downstream of pack failure.)
		// Exception: if the context was canceled, the pipe write error in PackDir
		// is a symptom of context cancellation, not an independent pack failure.
		// Surface ctx.Err() so callers can use errors.Is(err, context.Canceled).
		if resp != nil {
			_ = resp.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return dto.UploadResponse{}, ctxErr
		}
		return dto.UploadResponse{}, packErr
	}
	if doErr != nil {
		return dto.UploadResponse{}, fmt.Errorf("http do: %w", doErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return dto.UploadResponse{}, decodeUploadError(resp)
	}

	var out dto.UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return dto.UploadResponse{}, fmt.Errorf("decode response: %w", err)
	}
	_ = packResult // available to T18 if needed
	return out, nil
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
