package httpapi

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// These reject before any database work, so they need no pool.
func TestAdminExportRejectsBadFieldSelections(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", AdminAllowedNets: testAdminNets, Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"unknown column", "?fields=prompt,haiku", "unknown_field"},
		// An empty selection would otherwise produce a file with no columns,
		// which reads as a broken export rather than an empty one.
		{"only separators", "?fields=,,,", "no_fields"},
		// A SQL fragment is just an unknown column name; nothing is
		// interpolated from the request. Percent-encoded because a raw space
		// is not a legal request target.
		{"injection attempt", "?fields=prompt,%28SELECT%201%29", "unknown_field"},
		{"injection via drop", "?fields=prompt%3Bdrop+table+questions", "unknown_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/questions/export.csv"+tc.query, ""))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), tc.want) {
				t.Fatalf("body = %s, want code %q", res.Body.String(), tc.want)
			}
		})
	}
}

// The checkbox list is built from this, so every name it offers has to be one
// the export itself accepts.
func TestAdminExportFieldsMatchTheExport(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", AdminAllowedNets: testAdminNets, Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet, "/api/admin/questions/export-fields", ""))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"prompt"`) {
		t.Fatalf("field list does not mention prompt: %s", res.Body.String())
	}
	for _, name := range defaultExportFields {
		if !knownExportField(name) {
			t.Errorf("default field %q is not an exportable column", name)
		}
	}
}

func TestAdminExportWritesTheWholeLibrary(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	s := &Server{Pool: pool, AdminPassword: "correct horse", AdminAllowedNets: testAdminNets,
		Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}, Timezone: time.UTC}
	handler := s.Handler()

	const slug = "httpapi-export-fixture"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id IN (SELECT id FROM topics WHERE slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE slug = $1`, slug)
	})

	payload := `{"topics":[{"slug":"` + slug + `","name":"Export Fixture","questions":[
		{"external_id":"export-1","difficulty":9,"prompt":"Prompt, with a comma","answer":"Answer",
		 "aliases":["Answer Two"],"distractors":["Wrong one","Wrong two","Wrong three"]}]}]}`
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", payload))
	if res.Code != http.StatusOK {
		t.Fatalf("seed the fixture: %d (%s)", res.Code, res.Body.String())
	}
	hideTopic(t, pool, slug)

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet,
		"/api/admin/questions/export.csv?fields=topic_slug,prompt,answer,distractors,external_id", ""))
	if res.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200", res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	// Without this the browser renders the CSV instead of saving it, and the
	// "link" stops being a download.
	if cd := res.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}

	records, err := csv.NewReader(strings.NewReader(res.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("got %d rows, want a header and at least the fixture", len(records))
	}

	// Columns come back in exportColumns order, not the order they were asked
	// for, so two exports of one selection always line up.
	want := []string{"external_id", "topic_slug", "prompt", "answer", "distractors"}
	if strings.Join(records[0], ",") != strings.Join(want, ",") {
		t.Fatalf("header = %v, want %v", records[0], want)
	}

	var found []string
	for _, row := range records[1:] {
		if row[1] == slug {
			found = row
		}
	}
	if found == nil {
		t.Fatal("the fixture question is missing from the export")
	}
	// A comma inside a cell must survive the round trip, which is the whole
	// reason for using encoding/csv rather than joining strings.
	if found[2] != "Prompt, with a comma" {
		t.Errorf("prompt = %q, want the comma preserved", found[2])
	}
	if found[4] != "Wrong one | Wrong two | Wrong three" {
		t.Errorf("distractors = %q, want them pipe-joined", found[4])
	}
	if found[0] != "export-1" {
		t.Errorf("external_id = %q, want export-1", found[0])
	}
}

// The export is the "everything" button: it must not inherit the listing's
// filters, or a filtered file would look like the whole library.
func TestAdminExportIgnoresListingFilters(t *testing.T) {
	pool := testsupport.MustPool(t)
	ctx := context.Background()
	s := &Server{Pool: pool, AdminPassword: "correct horse", AdminAllowedNets: testAdminNets,
		Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}, Timezone: time.UTC}
	handler := s.Handler()

	const slug = "httpapi-export-unfiltered"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM questions WHERE topic_id IN (SELECT id FROM topics WHERE slug = $1)`, slug)
		_, _ = pool.Exec(ctx, `DELETE FROM topics WHERE slug = $1`, slug)
	})

	payload := `{"topics":[{"slug":"` + slug + `","name":"Unfiltered Fixture","questions":[
		{"external_id":"unfiltered-1","difficulty":2,"prompt":"P","answer":"A",
		 "distractors":["x","y","z"]}]}]}`
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodPost, "/api/admin/questions/import", payload))
	if res.Code != http.StatusOK {
		t.Fatalf("seed the fixture: %d (%s)", res.Code, res.Body.String())
	}
	hideTopic(t, pool, slug)

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, signedRequest(s, http.MethodGet,
		"/api/admin/questions/export.csv?fields=topic_slug&topic=something-else&difficulty=hard&search=zzz", ""))
	if res.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200", res.Code)
	}
	if !strings.Contains(res.Body.String(), slug) {
		t.Error("filter parameters removed rows from the export")
	}
}
