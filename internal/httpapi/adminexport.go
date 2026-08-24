package httpapi

import (
	"encoding/csv"
	"net/http"
	"strings"
)

// exportColumn is one selectable CSV column: the header an operator sees and
// the SQL that produces it. Every expression yields text so the whole row can
// be scanned into a []string and handed straight to encoding/csv.
type exportColumn struct {
	Name string
	SQL  string
}

// multiValueSeparator joins the repeated fields -- aliases and distractors --
// inside a single cell. A pipe rather than a comma, because a comma inside a
// quoted CSV cell reads as part of the text and is easy to mistake for a
// column boundary when the file is eyeballed rather than parsed.
const multiValueSeparator = " | "

// exportColumns is also the column order: the CSV follows this sequence
// whatever order the fields arrive in, so two exports of the same selection
// always line up.
var exportColumns = []exportColumn{
	{"id", "q.id::text"},
	{"external_id", "COALESCE(q.external_id, '')"},
	{"topic_slug", "t.slug"},
	{"topic_name", "t.name"},
	{"difficulty", "q.difficulty::text"},
	{"difficulty_rating", "q.difficulty_rating::text"},
	{"prompt", "q.prompt"},
	{"answer", "q.canonical_answer"},
	{"aliases", `COALESCE((SELECT string_agg(a.alias, '` + multiValueSeparator +
		`' ORDER BY a.id) FROM question_aliases a WHERE a.question_id = q.id), '')`},
	{"distractors", `COALESCE((SELECT string_agg(d.option_text, '` + multiValueSeparator +
		`' ORDER BY d.id) FROM question_distractors d WHERE d.question_id = q.id), '')`},
	{"status", "q.status::text"},
	{"used_count", "(SELECT count(*) FROM daily_puzzle_questions dpq WHERE dpq.question_id = q.id)::text"},
	{"last_used", "COALESCE((SELECT max(dpq.puzzle_date) FROM daily_puzzle_questions dpq WHERE dpq.question_id = q.id)::text, '')"},
}

// defaultExportFields is what the panel ticks before anyone touches it: enough
// to read and edit the content, without the bookkeeping columns.
var defaultExportFields = []string{
	"topic_name", "difficulty", "difficulty_rating", "prompt", "answer", "aliases", "distractors",
}

// adminExportQuestions writes the whole library as CSV. It deliberately
// ignores the listing's filters and paging: this is the "give me everything"
// button, and a partial file that looks complete is worse than no file.
func (s *Server) adminExportQuestions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	wanted := defaultExportFields
	if raw := r.URL.Query().Get("fields"); raw != "" {
		wanted = strings.Split(raw, ",")
	}
	chosen := map[string]bool{}
	for _, field := range wanted {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if !knownExportField(field) {
			s.fail(w, http.StatusBadRequest, "unknown_field", "No column called "+field+".")
			return
		}
		chosen[field] = true
	}
	if len(chosen) == 0 {
		s.fail(w, http.StatusBadRequest, "no_fields", "Pick at least one column to export.")
		return
	}

	// Selection is filtered through exportColumns rather than interpolated
	// from the request, so the query text only ever contains SQL this file
	// wrote.
	headers := make([]string, 0, len(chosen))
	expressions := make([]string, 0, len(chosen))
	for _, column := range exportColumns {
		if chosen[column.Name] {
			headers = append(headers, column.Name)
			expressions = append(expressions, column.SQL)
		}
	}

	rows, err := s.Pool.Query(r.Context(), `
		SELECT `+strings.Join(expressions, ", ")+`
		FROM questions q
		JOIN topics t ON t.id = q.topic_id
		ORDER BY t.slug, q.difficulty_rating, q.id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()

	// Past this point the response is committed: the header is written before
	// the first row, so a later failure can only be logged, not turned into an
	// error status.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="trivial-questions-`+s.date(s.now()).String()+`.csv"`)
	w.WriteHeader(http.StatusOK)

	out := csv.NewWriter(w)
	if err := out.Write(headers); err != nil {
		s.logger().Error("write csv header", "error", err)
		return
	}

	record := make([]string, len(headers))
	targets := make([]any, len(headers))
	for i := range record {
		targets[i] = &record[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			s.logger().Error("scan csv row", "error", err)
			return
		}
		if err := out.Write(record); err != nil {
			s.logger().Error("write csv row", "error", err)
			return
		}
	}
	if err := rows.Err(); err != nil {
		s.logger().Error("read csv rows", "error", err)
		return
	}
	out.Flush()
	if err := out.Error(); err != nil {
		s.logger().Error("flush csv", "error", err)
	}
}

func knownExportField(name string) bool {
	for _, column := range exportColumns {
		if column.Name == name {
			return true
		}
	}
	return false
}

// adminExportFields tells the panel which columns exist and which are ticked
// by default, so the checkbox list cannot drift from what the export accepts.
func (s *Server) adminExportFields(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	type field struct {
		Name       string `json:"name"`
		ByDefault  bool   `json:"by_default"`
		MultiValue bool   `json:"multi_value"`
	}
	defaults := map[string]bool{}
	for _, name := range defaultExportFields {
		defaults[name] = true
	}
	out := make([]field, 0, len(exportColumns))
	for _, column := range exportColumns {
		out = append(out, field{
			Name:       column.Name,
			ByDefault:  defaults[column.Name],
			MultiValue: column.Name == "aliases" || column.Name == "distractors",
		})
	}
	s.write(w, http.StatusOK, map[string]any{
		"fields":    out,
		"separator": multiValueSeparator,
	})
}
