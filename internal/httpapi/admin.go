package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/puzzle"
)

// defaultAdminHorizon is how many days ahead the panel shows when the caller
// does not say.
const defaultAdminHorizon = 14

// maxAdminHorizon bounds both the listing and a generate request, so a typo in
// a query string cannot ask for a decade of boards.
const maxAdminHorizon = 60

type adminSlot struct {
	Position  int     `json:"position"`
	Pinned    bool    `json:"pinned"`
	TopicSlug *string `json:"topic_slug"`
	TopicName *string `json:"topic_name"`
}

type adminDay struct {
	Date      clock.Date  `json:"date"`
	Generated bool        `json:"generated"`
	HasRuns   bool        `json:"has_runs"`
	Editable  bool        `json:"editable"`
	Slots     []adminSlot `json:"slots"`
}

type adminTopic struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Active          bool   `json:"active"`
	SelectionWeight int    `json:"selection_weight"`
}

// adminGeneratorFor builds a generator bound to a transaction, using the same
// settings the CLI uses.
func (s *Server) adminGeneratorFor(tx pgx.Tx) puzzle.Generator {
	set := s.puzzleSettings()
	return puzzle.Generator{
		DB:                 tx,
		CooldownDays:       set.CooldownDays,
		AnswerCooldownDays: set.AnswerCooldownDays,
		TimeLimitSeconds:   set.TimeLimitSeconds,
	}
}

func (s *Server) adminPuzzles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	days, ok := horizonParam(w, r.URL.Query().Get("days"))
	if !ok {
		return
	}
	today := s.date(s.now())

	out := make([]adminDay, 0, days)
	for i := 0; i < days; i++ {
		date := today.AddDays(i)
		day, err := s.loadAdminDay(r.Context(), date, today)
		if err != nil {
			s.internal(w, err)
			return
		}
		out = append(out, day)
	}
	s.write(w, http.StatusOK, map[string]any{"days": out})
}

// loadAdminDay reads one date through the pool. Reads only, so no transaction
// is needed.
func (s *Server) loadAdminDay(ctx context.Context, date, today clock.Date) (adminDay, error) {
	board, err := puzzle.Get(ctx, s.Pool, date)
	if err != nil {
		return adminDay{}, err
	}
	pins, err := puzzle.Pins(ctx, s.Pool, date)
	if err != nil {
		return adminDay{}, err
	}
	var runs int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM runs WHERE puzzle_date = $1`, date).Scan(&runs); err != nil {
		return adminDay{}, err
	}

	generatedAt := map[int]puzzle.Entry{}
	if board != nil {
		for _, e := range board.Entries {
			generatedAt[e.TopicPosition] = e
		}
	}

	day := adminDay{
		Date:      date,
		Generated: board != nil,
		HasRuns:   runs > 0,
		Editable:  today.Before(date) && runs == 0,
		Slots:     make([]adminSlot, 0, 3),
	}
	for position := 0; position < 3; position++ {
		slot := adminSlot{Position: position}
		if pinned, ok := pins[position]; ok {
			slot.Pinned = true
			slug, name := pinned.Slug, pinned.Name
			slot.TopicSlug, slot.TopicName = &slug, &name
		} else if e, ok := generatedAt[position]; ok {
			slug, name := e.TopicSlug, e.TopicName
			slot.TopicSlug, slot.TopicName = &slug, &name
		}
		day.Slots = append(day.Slots, slot)
	}
	return day, nil
}

func horizonParam(w http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		return defaultAdminHorizon, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxAdminHorizon {
		sFail(w, http.StatusBadRequest, "invalid_request",
			"days must be a whole number between 1 and "+strconv.Itoa(maxAdminHorizon)+".")
		return 0, false
	}
	return n, true
}

func adminDateParam(w http.ResponseWriter, r *http.Request) (clock.Date, bool) {
	date, err := clock.ParseDate(r.PathValue("date"))
	if err != nil {
		sFail(w, http.StatusBadRequest, "invalid_request", "Invalid date; use YYYY-MM-DD.")
		return clock.Date{}, false
	}
	return date, true
}

// adminSetTopics replaces the pins for a date and rebuilds it.
func (s *Server) adminSetTopics(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	date, ok := adminDateParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Slots []struct {
			Position  int     `json:"position"`
			TopicSlug *string `json:"topic_slug"`
		} `json:"slots"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// A null slug means "leave this position automatic", so it contributes no
	// pin rather than an error.
	wanted := map[int]string{}
	for _, slot := range body.Slots {
		if slot.Position < 0 || slot.Position > 2 {
			s.fail(w, http.StatusBadRequest, "invalid_request", "Slot positions run 0 to 2.")
			return
		}
		if slot.TopicSlug == nil || *slot.TopicSlug == "" {
			continue
		}
		wanted[slot.Position] = *slot.TopicSlug
	}
	seen := map[string]bool{}
	for _, slug := range wanted {
		if seen[slug] {
			s.fail(w, http.StatusBadRequest, "duplicate_topic",
				"A topic can only take one slot on a board.")
			return
		}
		seen[slug] = true
	}

	s.rebuildWithPins(w, r, date, wanted)
}

// adminClearTopics drops every pin for a date, returning it to fully automatic
// selection, and rebuilds.
func (s *Server) adminClearTopics(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	date, ok := adminDateParam(w, r)
	if !ok {
		return
	}
	s.rebuildWithPins(w, r, date, map[int]string{})
}

func (s *Server) rebuildWithPins(w http.ResponseWriter, r *http.Request, date clock.Date, wanted map[int]string) {
	ctx := r.Context()
	today := s.date(s.now())

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slots := map[int]int64{}
	for position, slug := range wanted {
		var id int64
		err := tx.QueryRow(ctx, `SELECT id FROM topics WHERE slug = $1`, slug).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			s.fail(w, http.StatusBadRequest, "unknown_topic", "No topic called "+slug+".")
			return
		}
		if err != nil {
			s.internal(w, err)
			return
		}
		slots[position] = id
	}

	if err := puzzle.SetPins(ctx, tx, date, slots); err != nil {
		s.internal(w, err)
		return
	}
	if _, err := s.adminGeneratorFor(tx).Rebuild(ctx, date, today); err != nil {
		s.rebuildError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, err)
		return
	}

	day, err := s.loadAdminDay(ctx, date, today)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, day)
}

// rebuildError translates the generator's typed failures into codes the panel
// can act on. The starved-pin message is the whole explanation an operator
// gets, so it names the topic and the difficulty.
func (s *Server) rebuildError(w http.ResponseWriter, err error) {
	var starved *puzzle.PinnedTopicStarvedError
	var thin *puzzle.InsufficientContentError
	switch {
	case errors.Is(err, puzzle.ErrNotFuture):
		s.fail(w, http.StatusConflict, "date_not_future",
			"Only upcoming dates can be changed.")
	case errors.Is(err, puzzle.ErrDateHasRuns):
		s.fail(w, http.StatusConflict, "date_has_runs",
			"Someone has already played this date, so its board cannot change.")
	case errors.As(err, &starved):
		s.fail(w, http.StatusConflict, "pinned_topic_starved", starved.Error())
	case errors.As(err, &thin):
		s.fail(w, http.StatusConflict, "insufficient_content", thin.Error())
	default:
		s.internal(w, err)
	}
}

// adminGenerate fills in dates that do not exist yet, one transaction each so
// one starved date does not discard the rest. This differs from the CLI, which
// aborts the whole run on the first failure.
func (s *Server) adminGenerate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		Days int `json:"days"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.Days == 0 {
		body.Days = defaultAdminHorizon
	}
	if body.Days < 1 || body.Days > maxAdminHorizon {
		s.fail(w, http.StatusBadRequest, "invalid_request",
			"days must be a whole number between 1 and "+strconv.Itoa(maxAdminHorizon)+".")
		return
	}

	ctx := r.Context()
	today := s.date(s.now())
	type failure struct {
		Date    clock.Date `json:"date"`
		Message string     `json:"message"`
	}
	generated := []clock.Date{}
	skipped := []clock.Date{}
	failed := []failure{}

	for i := 0; i < body.Days; i++ {
		date := today.AddDays(i)
		existing, err := puzzle.Get(ctx, s.Pool, date)
		if err != nil {
			s.internal(w, err)
			return
		}
		if existing != nil {
			skipped = append(skipped, date)
			continue
		}
		if err := s.generateOne(ctx, date); err != nil {
			failed = append(failed, failure{Date: date, Message: err.Error()})
			continue
		}
		generated = append(generated, date)
	}

	s.write(w, http.StatusOK, map[string]any{
		"generated": generated,
		"skipped":   skipped,
		"failed":    failed,
	})
}

func (s *Server) generateOne(ctx context.Context, date clock.Date) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := s.adminGeneratorFor(tx).GenerateFor(ctx, date); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) adminTopics(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	topics, err := content.AllTopics(r.Context(), s.Pool)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := make([]adminTopic, 0, len(topics))
	for _, t := range topics {
		out = append(out, adminTopic{Slug: t.Slug, Name: t.Name, Active: t.Active, SelectionWeight: t.SelectionWeight})
	}
	s.write(w, http.StatusOK, map[string]any{"topics": out})
}

func (s *Server) adminUpdateTopic(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body struct {
		SelectionWeight *int  `json:"selection_weight"`
		Active          *bool `json:"active"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.SelectionWeight != nil && (*body.SelectionWeight < 1 || *body.SelectionWeight > 1000) {
		s.fail(w, http.StatusBadRequest, "invalid_weight", "Weight must be between 1 and 1000.")
		return
	}

	t, err := content.UpdateTopicSettings(r.Context(), s.Pool, r.PathValue("slug"), body.SelectionWeight, body.Active)
	switch {
	case errors.Is(err, content.ErrNoSuchTopic):
		s.fail(w, http.StatusNotFound, "unknown_topic", "No topic with that slug.")
		return
	case errors.Is(err, content.ErrTooFewActiveTopics):
		s.fail(w, http.StatusConflict, "too_few_topics",
			"At least three topics must stay active or no board can be generated.")
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, adminTopic{Slug: t.Slug, Name: t.Name, Active: t.Active, SelectionWeight: t.SelectionWeight})
}
