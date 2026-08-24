// Package httpapi exposes the daily game over HTTP.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/play"
	"github.com/hjordan6/trivial/internal/puzzle"
)

const playerCookie = "trivial_player"

type Server struct {
	Pool            *pgxpool.Pool
	Clock           clock.Clock
	Timezone        *time.Location
	Logger          *slog.Logger
	CookieSecure    bool
	DevelopmentMode bool
	Assets          fs.FS
	// AdminPassword gates every /api/admin route. Empty disables the admin
	// surface entirely, which is what a zero-valued Server gets.
	AdminPassword string
	// CooldownDays and TimeLimitSeconds configure boards the admin panel
	// generates or rebuilds. They mirror the CLI's generator settings.
	CooldownDays     int
	TimeLimitSeconds int
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type envelope struct {
	ServerTime time.Time      `json:"server_time"`
	Puzzle     *puzzle.Puzzle `json:"puzzle,omitempty"`
	Run        *play.Run      `json:"run,omitempty"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/runs/current", s.current)
	mux.HandleFunc("POST /api/runs", s.start)
	mux.HandleFunc("POST /api/runs/{id}/finish", s.finish)
	mux.HandleFunc("POST /api/runs/{id}/questions/{qid}/reveal-options", s.reveal)
	mux.HandleFunc("POST /api/runs/{id}/questions/{qid}/answer", s.answer)
	mux.HandleFunc("POST /api/runs/{id}/share", s.share)
	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("POST /api/dev/reset", s.resetCurrentRun)

	mux.HandleFunc("POST /api/admin/login", s.adminLogin)
	mux.HandleFunc("POST /api/admin/logout", s.adminLogout)
	mux.HandleFunc("GET /api/admin/session", s.adminSession)
	mux.HandleFunc("GET /api/admin/puzzles", s.adminPuzzles)
	mux.HandleFunc("PUT /api/admin/puzzles/{date}/topics", s.adminSetTopics)
	mux.HandleFunc("DELETE /api/admin/puzzles/{date}/topics", s.adminClearTopics)
	mux.HandleFunc("POST /api/admin/puzzles/generate", s.adminGenerate)
	mux.HandleFunc("GET /api/admin/topics", s.adminTopics)
	mux.HandleFunc("PATCH /api/admin/topics/{slug}", s.adminUpdateTopic)
	mux.HandleFunc("GET /api/admin/questions", s.adminQuestions)
	mux.HandleFunc("POST /api/admin/questions/import", s.adminImportQuestions)
	mux.HandleFunc("GET /api/admin/questions/export-fields", s.adminExportFields)
	mux.HandleFunc("GET /api/admin/questions/export.csv", s.adminExportQuestions)
	mux.HandleFunc("POST /api/admin/questions", s.adminCreateQuestion)
	mux.HandleFunc("PUT /api/admin/questions/{id}", s.adminUpdateQuestion)

	if s.Assets != nil {
		assets, err := fs.Sub(s.Assets, "dist")
		if err != nil {
			panic(err)
		}
		mux.Handle("GET /", spaHandler(assets))
	}
	return mux
}

func (s *Server) resetCurrentRun(w http.ResponseWriter, r *http.Request) {
	if !s.DevelopmentMode {
		s.fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	playerID := s.optionalPlayer(r)
	if playerID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := s.Pool.Exec(r.Context(), `DELETE FROM runs WHERE player_id=$1 AND puzzle_date=$2`, playerID, s.date(s.now())); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) now() time.Time {
	if s.Clock == nil {
		return time.Now()
	}
	return s.Clock.Now()
}
func (s *Server) date(now time.Time) clock.Date {
	loc := s.Timezone
	if loc == nil {
		loc = time.UTC
	}
	return clock.PuzzleDateAt(now, loc)
}

func (s *Server) optionalPlayer(r *http.Request) string {
	c, err := r.Cookie(playerCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) requirePlayer(w http.ResponseWriter, r *http.Request) (string, bool) {
	now := s.now()
	id := s.optionalPlayer(r)
	if id != "" {
		if err := play.TouchPlayer(r.Context(), s.Pool, id, now); err == nil {
			return id, true
		}
	}
	id, err := play.CreatePlayer(r.Context(), s.Pool, now)
	if err != nil {
		s.internal(w, err)
		return "", false
	}
	http.SetCookie(w, &http.Cookie{Name: playerCookie, Value: id, Path: "/", HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60})
	return id, true
}

func (s *Server) current(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	p, err := puzzle.Get(r.Context(), s.Pool, s.date(now))
	if err != nil {
		s.internal(w, err)
		return
	}
	if p == nil {
		s.fail(w, http.StatusServiceUnavailable, "no_puzzle", "Today's puzzle is not available.")
		return
	}
	id := s.optionalPlayer(r)
	if id == "" {
		s.write(w, http.StatusOK, envelope{ServerTime: now, Puzzle: startPuzzle(p)})
		return
	}
	run, err := play.Current(r.Context(), s.Pool, id, p.Date, now)
	if errors.Is(err, pgx.ErrNoRows) {
		run = nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, envelope{ServerTime: now, Puzzle: p, Run: run})
}

// startPuzzle exposes only the date, limit, and three topic names. Sending the
// prompts before Start would let a player read the board before the clock runs.
func startPuzzle(p *puzzle.Puzzle) *puzzle.Puzzle {
	out := &puzzle.Puzzle{Date: p.Date, TimeLimitSeconds: p.TimeLimitSeconds}
	seen := make(map[int]bool, 3)
	for _, entry := range p.Entries {
		if seen[entry.TopicPosition] {
			continue
		}
		seen[entry.TopicPosition] = true
		entry.QuestionID = 0
		entry.Prompt = ""
		out.Entries = append(out.Entries, entry)
	}
	return out
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	now := s.now()
	p, err := puzzle.Get(r.Context(), s.Pool, s.date(now))
	if err != nil {
		s.internal(w, err)
		return
	}
	if p == nil {
		s.fail(w, 503, "no_puzzle", "Today's puzzle is not available.")
		return
	}
	var body struct {
		ReferredByRunID *string `json:"referred_by_run_id"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, 400, "invalid_request", err.Error())
		return
	}
	run, err := play.Start(r.Context(), s.Pool, id, p, now, body.ReferredByRunID)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, envelope{ServerTime: now, Puzzle: p, Run: run})
}

func (s *Server) finish(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	now := s.now()
	if err := play.Finish(r.Context(), s.Pool, r.PathValue("id"), id, now); err != nil {
		s.playError(w, err)
		return
	}
	run, err := play.Load(r.Context(), s.Pool, r.PathValue("id"), id, now)
	if err != nil {
		s.playError(w, err)
		return
	}
	s.write(w, 200, envelope{ServerTime: now, Puzzle: run.Puzzle, Run: run})
}

func questionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("qid"), 10, 64)
	if err != nil {
		sFail(w, 400, "invalid_request", "Invalid question id.")
		return 0, false
	}
	return id, true
}

func (s *Server) reveal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	qid, ok := questionID(w, r)
	if !ok {
		return
	}
	options, err := play.Reveal(r.Context(), s.Pool, r.PathValue("id"), id, qid, s.now())
	if err != nil {
		s.playError(w, err)
		return
	}
	s.write(w, 200, map[string]any{"options": options})
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	qid, ok := questionID(w, r)
	if !ok {
		return
	}
	var body struct {
		Stage  play.Stage `json:"stage"`
		Answer string     `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.fail(w, 400, "invalid_request", "Invalid JSON body.")
		return
	}
	a, result, err := play.AnswerQuestion(r.Context(), s.Pool, r.PathValue("id"), id, qid, body.Stage, strings.TrimSpace(body.Answer), s.now())
	if err != nil {
		s.playError(w, err)
		return
	}
	if result != nil && result.NearMiss {
		s.logger().Info("near-miss answer", "question_id", qid, "submission", body.Answer, "matched_alias", result.Matched)
	}
	s.write(w, 200, a)
}

func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}
	var body struct {
		Nickname *string `json:"nickname"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, 400, "invalid_request", err.Error())
		return
	}
	var completed *time.Time
	if err := s.Pool.QueryRow(r.Context(), `SELECT completed_at FROM runs WHERE id=$1 AND player_id=$2`, r.PathValue("id"), id).Scan(&completed); errors.Is(err, pgx.ErrNoRows) {
		s.playError(w, play.ErrNotYourRun)
		return
	} else if err != nil {
		s.internal(w, err)
		return
	}
	if completed == nil {
		s.fail(w, 409, "run_incomplete", "Finish the run before sharing.")
		return
	}
	token, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	var stable string
	err = s.Pool.QueryRow(r.Context(), `INSERT INTO share_tokens(token,run_id,nickname) VALUES($1,$2,$3) ON CONFLICT(run_id) DO UPDATE SET nickname=EXCLUDED.nickname RETURNING token`, token, r.PathValue("id"), cleanNickname(body.Nickname)).Scan(&stable)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, 200, map[string]string{"token": stable, "url": "/c/" + stable})
}

type Stats struct {
	DaysPlayed        int     `json:"days_played"`
	ScoreDistribution [10]int `json:"score_distribution"`
	CurrentStreak     int     `json:"current_streak"`
	LongestStreak     int     `json:"longest_streak"`
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	id := s.optionalPlayer(r)
	if id == "" {
		s.write(w, 200, Stats{})
		return
	}
	rows, err := s.Pool.Query(r.Context(), `SELECT r.puzzle_date,count(ra.outcome) FILTER(WHERE ra.outcome IN('star','circle')) FROM runs r LEFT JOIN run_answers ra ON ra.run_id=r.id WHERE r.player_id=$1 AND r.completed_at IS NOT NULL GROUP BY r.puzzle_date ORDER BY r.puzzle_date`, id)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	var dates []clock.Date
	var out Stats
	for rows.Next() {
		var d clock.Date
		var score int
		if err := rows.Scan(&d, &score); err != nil {
			s.internal(w, err)
			return
		}
		dates = append(dates, d)
		out.DaysPlayed++
		out.ScoreDistribution[score]++
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	out.CurrentStreak, out.LongestStreak = streaks(dates, s.date(s.now()))
	s.write(w, 200, out)
}

func streaks(dates []clock.Date, today clock.Date) (current, longest int) {
	if len(dates) == 0 {
		return
	}
	run := 0
	for i, d := range dates {
		if i > 0 && dates[i-1].AddDays(1).Equal(d) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}
	last := dates[len(dates)-1]
	if last.Equal(today) || last.Equal(today.AddDays(-1)) {
		current = run
	}
	return
}

func (s *Server) playError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, play.ErrNotYourRun):
		s.fail(w, 403, "not_your_run", "This run belongs to another player.")
	case errors.Is(err, play.ErrExpired):
		s.fail(w, 409, "run_expired", "The clock has run out.")
	case errors.Is(err, play.ErrAlreadyAnswered):
		s.fail(w, 409, "already_answered", "This question is already resolved.")
	case errors.Is(err, play.ErrInvalidStage):
		s.fail(w, 409, "invalid_stage", "That answer stage is no longer available.")
	default:
		s.internal(w, err)
	}
}

// spaHandler serves the built frontend, falling back to index.html for paths
// that are not files on disk. Without the fallback a client-side route such as
// /admin gets a plain-text 404 from http.FileServer, which only ever looks for
// a file of that name.
//
// A miss under /assets/ stays a 404 on purpose: those URLs are content-hashed
// and always real, so answering one with HTML would turn a broken deploy into a
// confusing script parse error instead of an obvious missing file.
func spaHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			files.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(assets, name); err == nil {
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		shell, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(shell)
	})
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	s.logger().Error("request failed", "error", err)
	s.fail(w, 500, "internal_error", "Something went wrong.")
}
func (s *Server) fail(w http.ResponseWriter, status int, code, msg string) {
	sFail(w, status, code, msg)
}
func sFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{code, msg})
}
func (s *Server) write(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}
func randomToken() (string, error) {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func cleanNickname(v *string) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return &s
}

var _ context.Context
