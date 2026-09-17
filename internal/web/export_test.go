package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
	"waldi/internal/store"
)

func TestWriteExportZip(t *testing.T) {
	published := time.Date(2026, time.June, 20, 22, 0, 0, 0, time.UTC)
	posts := []store.Post{
		{
			Title:       "Tips & <script>alert(1)</script>",
			Slug:        "tips",
			HTML:        "<p>body</p>",
			Status:      "published",
			BlogLang:    "fa",
			Doc:         json.RawMessage(`{"type":"doc"}`),
			PublishedAt: &published,
			CreatedAt:   published,
		},
		{
			Title:     "Unfinished",
			Slug:      "unfinished",
			HTML:      "<p>wip</p>",
			Status:    "draft",
			BlogLang:  "en",
			CreatedAt: published,
		},
	}

	var buf bytes.Buffer
	if err := writeExportZip(&buf, posts); err != nil {
		t.Fatalf("writeExportZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		files[f.Name] = string(b)
	}

	var dump []exportPost
	if err := json.Unmarshal([]byte(files["posts.json"]), &dump); err != nil {
		t.Fatalf("posts.json: %v", err)
	}
	if len(dump) != 2 || dump[0].Slug != "tips" {
		t.Fatalf("unexpected posts.json: %+v", dump)
	}

	pub, ok := files["posts/2026-06-20-tips.html"]
	if !ok {
		t.Fatalf("missing published post file, got %v", zr.File)
	}
	if strings.Contains(pub, "<script>") || !strings.Contains(pub, "Tips &amp; &lt;script&gt;") {
		t.Fatalf("title not escaped: %s", pub)
	}
	if !strings.Contains(pub, `<html lang="fa" dir="rtl">`) || !strings.Contains(pub, "<p>body</p>") {
		t.Fatalf("unexpected published post html: %s", pub)
	}

	draft, ok := files["posts/draft-unfinished.html"]
	if !ok {
		t.Fatalf("missing draft file")
	}
	if !strings.Contains(draft, `<html lang="en" dir="ltr">`) {
		t.Fatalf("unexpected draft html: %s", draft)
	}
}
