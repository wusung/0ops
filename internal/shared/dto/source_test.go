package dto

import (
	"encoding/json"
	"testing"
)

func TestSource_MarshalRoundtrip_GitHub(t *testing.T) {
	in := Source{Type: SourceKindGitHub, GitHub: &SourceGitHub{URL: "https://github.com/foo/bar", Ref: "main"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Source
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != SourceKindGitHub || out.GitHub == nil || out.GitHub.URL != "https://github.com/foo/bar" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
	if out.Upload != nil {
		t.Fatalf("unexpected Upload set on github source")
	}
}

func TestSource_MarshalRoundtrip_Upload(t *testing.T) {
	in := Source{Type: SourceKindUpload, Upload: &SourceUpload{UploadID: "upl_test", Ref: "main"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Source
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != SourceKindUpload || out.Upload == nil || out.Upload.UploadID != "upl_test" {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
}
