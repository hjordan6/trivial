package content_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/hjordan6/trivial/internal/content"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// seedTopics creates n topics at weight 1 and returns their slugs.
func seedTopics(t *testing.T, tx pgx.Tx, n int) []string {
	t.Helper()
	ctx := context.Background()
	slugs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("settings-%d", i)
		if _, err := content.UpsertTopic(ctx, tx, slug, fmt.Sprintf("Settings %d", i)); err != nil {
			t.Fatalf("UpsertTopic(%s): %v", slug, err)
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

func TestUpdateTopicSettingsChangesWeightAndActive(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	slugs := seedTopics(t, tx, 4)

	weight := 7
	got, err := content.UpdateTopicSettings(ctx, tx, slugs[0], &weight, nil)
	if err != nil {
		t.Fatalf("UpdateTopicSettings: %v", err)
	}
	if got.SelectionWeight != 7 || !got.Active {
		t.Fatalf("got weight=%d active=%v, want weight=7 active=true", got.SelectionWeight, got.Active)
	}

	inactive := false
	got, err = content.UpdateTopicSettings(ctx, tx, slugs[0], nil, &inactive)
	if err != nil {
		t.Fatalf("UpdateTopicSettings(deactivate): %v", err)
	}
	// The nil weight must leave the earlier change alone.
	if got.SelectionWeight != 7 || got.Active {
		t.Fatalf("got weight=%d active=%v, want weight=7 active=false", got.SelectionWeight, got.Active)
	}
}

func TestUpdateTopicSettingsRejectsWeightOutOfRange(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	slugs := seedTopics(t, tx, 4)

	for _, weight := range []int{0, -1, 1001} {
		if _, err := content.UpdateTopicSettings(ctx, tx, slugs[0], &weight, nil); err == nil {
			t.Fatalf("weight %d was accepted, want an error", weight)
		}
	}
}

func TestUpdateTopicSettingsRefusesDeactivatingBelowThree(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	slugs := seedTopics(t, tx, 3)

	// Exactly three active topics exist, so deactivating any of them leaves a
	// library that cannot fill a board.
	inactive := false
	_, err := content.UpdateTopicSettings(ctx, tx, slugs[0], nil, &inactive)
	if !errors.Is(err, content.ErrTooFewActiveTopics) {
		t.Fatalf("err = %v, want ErrTooFewActiveTopics", err)
	}

	// A fourth topic makes the same deactivation legal.
	if _, err := content.UpsertTopic(ctx, tx, "settings-spare", "Spare"); err != nil {
		t.Fatalf("UpsertTopic: %v", err)
	}
	if _, err := content.UpdateTopicSettings(ctx, tx, slugs[0], nil, &inactive); err != nil {
		t.Fatalf("UpdateTopicSettings after adding a spare: %v", err)
	}
}

func TestUpdateTopicSettingsUnknownSlug(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	weight := 5
	_, err := content.UpdateTopicSettings(context.Background(), tx, "does-not-exist", &weight, nil)
	if !errors.Is(err, content.ErrNoSuchTopic) {
		t.Fatalf("err = %v, want ErrNoSuchTopic", err)
	}
}

func TestAllTopicsIncludesInactiveOnes(t *testing.T) {
	tx := testsupport.Tx(t, testsupport.MustPool(t))
	ctx := context.Background()
	slugs := seedTopics(t, tx, 4)

	inactive := false
	if _, err := content.UpdateTopicSettings(ctx, tx, slugs[3], nil, &inactive); err != nil {
		t.Fatalf("UpdateTopicSettings: %v", err)
	}

	active, err := content.ActiveTopics(ctx, tx)
	if err != nil {
		t.Fatalf("ActiveTopics: %v", err)
	}
	all, err := content.AllTopics(ctx, tx)
	if err != nil {
		t.Fatalf("AllTopics: %v", err)
	}
	if has(active, slugs[3]) {
		t.Fatalf("ActiveTopics included the deactivated %q", slugs[3])
	}
	if !has(all, slugs[3]) {
		t.Fatalf("AllTopics omitted the deactivated %q", slugs[3])
	}
	if len(all) != len(active)+1 {
		t.Fatalf("len(all)=%d len(active)=%d, want all to be one longer", len(all), len(active))
	}
}

func has(topics []content.Topic, slug string) bool {
	for _, t := range topics {
		if t.Slug == slug {
			return true
		}
	}
	return false
}
