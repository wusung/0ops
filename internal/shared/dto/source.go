package dto

// SourceKind is the discriminator for the Source sum type.
type SourceKind string

const (
	SourceKindGitHub SourceKind = "github"
	SourceKindUpload SourceKind = "upload"
)

// Source is a discriminated union representing the origin of application
// source code. Exactly one of GitHub or Upload should be non-nil, selected
// by the Type field.
type Source struct {
	Type   SourceKind    `json:"type"`
	GitHub *SourceGitHub `json:"github,omitempty"`
	Upload *SourceUpload `json:"upload,omitempty"`
}

// SourceGitHub identifies a GitHub repository and ref.
type SourceGitHub struct {
	URL string `json:"url"`
	Ref string `json:"ref"`
}

// SourceUpload references a pre-uploaded tarball by its upload ID.
type SourceUpload struct {
	UploadID string `json:"upload_id"`
	Ref      string `json:"ref,omitempty"`
}
