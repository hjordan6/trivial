package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJevSendsTheAnswerAsStateAndReadsScores(t *testing.T) {
	var got jevRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer key" {
			t.Errorf("Authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(`{"model":"jev-1.14.0","answers":{
			"acceptable":{"type":"noul","noul":0.93},
			"names_other_thing":{"type":"noul","noul":0.04},
			"too_vague":{"type":"noul","noul":0.19}},
			"usage":{"input_tokens":870,"output_tokens":0}}`))
	}))
	defer srv.Close()

	s, err := Jev{APIKey: "key", Endpoint: srv.URL}.Score(context.Background(), Question{
		Prompt:       "Its leaders included Rosa Parks.",
		Answer:       "The Civil Rights Movement",
		Accepted:     []string{"Civil Rights Movement"},
		WrongOptions: nil,
	}, "civil rights")
	if err != nil {
		t.Fatal(err)
	}
	if s.Acceptable != 0.93 || s.NamesOtherThing != 0.04 || s.TooVague != 0.19 || s.Model != "jev-1.14.0" {
		t.Errorf("scores = %+v", s)
	}
	if got.Model != jevModel {
		t.Errorf("model = %q, want the pinned %q", got.Model, jevModel)
	}
	if got.State.PlayerAnswer != "civil rights" || got.State.CorrectAnswer != "The Civil Rights Movement" {
		t.Errorf("state = %+v", got.State)
	}
	if got.State.WrongOptions == nil {
		t.Error("wrong_options sent as null, want []")
	}
	for _, name := range []string{"acceptable", "names_other_thing", "too_vague"} {
		if _, ok := got.Questions[name]; !ok {
			t.Errorf("request is missing question %q", name)
		}
	}
	// The player's text must not leak into the fixed questions.
	for name, q := range got.Questions {
		if strings.Contains(q.Instructions, "civil rights") {
			t.Errorf("question %q contains the player's answer", name)
		}
	}
}

// A reply without every score must fail rather than read the missing ones as
// zero, because zero on the two veto scores is the most permissive value.
func TestJevMissingScoreIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"jev-1.14.0","answers":{"acceptable":{"type":"noul","noul":0.99}}}`))
	}))
	defer srv.Close()
	if _, err := (Jev{APIKey: "key", Endpoint: srv.URL}).Score(context.Background(), Question{}, "x"); err == nil {
		t.Fatal("Score succeeded on a reply missing two scores")
	}
}

func TestJevHTTPErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"detail":"slow down"}`))
	}))
	defer srv.Close()
	_, err := Jev{APIKey: "key", Endpoint: srv.URL}.Score(context.Background(), Question{}, "x")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want a 429 error", err)
	}
}

func TestAccepts(t *testing.T) {
	tests := []struct {
		name string
		s    Scores
		want bool
	}{
		{"clear yes", Scores{Acceptable: 0.93, NamesOtherThing: 0.04, TooVague: 0.19}, true},
		{"at every limit", Scores{Acceptable: 0.65, NamesOtherThing: 0.35, TooVague: 0.45}, true},
		{"not acceptable enough", Scores{Acceptable: 0.6, NamesOtherThing: 0.04, TooVague: 0.1}, false},
		// Measured: "Berlin" for "The Berlin Wall".
		{"names something else", Scores{Acceptable: 0.88, NamesOtherThing: 0.49, TooVague: 0.21}, false},
		// Measured: "Mary" for "Typhoid Mary".
		{"too vague", Scores{Acceptable: 0.76, NamesOtherThing: 0.19, TooVague: 0.83}, false},
	}
	for _, tt := range tests {
		if got := tt.s.Accepts(DefaultThresholds); got != tt.want {
			t.Errorf("%s: Accepts = %v, want %v", tt.name, got, tt.want)
		}
	}
}
