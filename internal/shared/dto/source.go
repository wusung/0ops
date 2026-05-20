package dto

// SourceKind is the discriminator for the Source sum type.
type SourceKind string

const (
	SourceKindGitHub SourceKind = "github"
	SourceKindUpload SourceKind = "upload"
)

// Source is a discriminated union identifying where an app's code comes from.
// Exactly one of GitHub or Upload must be non-nil — enforced by the server's
// validation layer (see ADR-0013 §4.3 and the upcoming T11 normalization).
// Constructing a Source with both set or neither set is invalid and will be
// rejected with HTTP 422.
type Source struct {
	Type   SourceKind    `json:"type"`
	GitHub *SourceGitHub `json:"github,omitempty"`
	Upload *SourceUpload `json:"upload,omitempty"`
}

// SourceGitHub identifies a GitHub repository and ref.
type SourceGitHub struct {
	// URL is the canonical github.com URL of the repo.
	URL string `json:"url"`
	// Ref is a required branch, tag, or commit sha. Unlike SourceUpload.Ref
	// (optional audit tag), Ref here drives the GHA workflow checkout and
	// must not be omitted.
	Ref string `json:"ref"`
}

// SourceUpload references a pre-uploaded tarball by its upload ID.
type SourceUpload struct {
	UploadID string `json:"upload_id"`
	Ref      string `json:"ref,omitempty"`
}
