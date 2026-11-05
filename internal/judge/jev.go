package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// jevEndpoint is TypeSafe's System One API, which serves Jev.
const jevEndpoint = "https://api.typesafe.ai/v1/systemone"

// jevModel is pinned rather than "jev-latest" so that the thresholds, which
// were measured against this version, keep meaning what they meant. Moving it
// means re-running scripts/jev-judge-test.py first.
const jevModel = "jev-1.14.0"

// maxErrorBody caps how much of a failure response is carried into an error.
const maxErrorBody = 4 << 10

// The three questions put to Jev in every request. The player's answer is
// only ever data in the request's state; these never change, so nothing a
// player types can rewrite what Jev is asked.
//
// The examples are deliberately not answers from the test set in
// scripts/jev-judge-test.py, so that a test run measures the model rather than
// whether it can repeat the examples back. scripts/jev-judge-test.py carries
// the same wording; change both together.
var jevQuestions = map[string]jevQuestion{
	"acceptable": {
		Type: "noul",
		Instructions: "A player in a trivia game typed player_answer for this question. " +
			"Would a fair trivia host accept it as correct?",
		Criteria: map[string]string{
			"true": "player_answer names the same specific thing as correct_answer: a " +
				"shortened form, surname, nickname, alternate name, abbreviation, " +
				"number written as digits or words, or a spelling variant that could " +
				"not reasonably mean something else given the question.",
			"false": "player_answer names a different thing, is less specific than " +
				"correct_answer in a way the clues would also fit (such as 'bear' for " +
				"'polar bear' or 'Gemini' for 'Gemini 4'), matches one of " +
				"wrong_options, or is not an answer at all.",
		},
	},
	"names_other_thing": {
		Type: "noul",
		Instructions: "Ignore the question. Read player_answer completely on its own. Does it " +
			"name a real, well-known thing that is different from correct_answer, " +
			"such as a city, country, person, substance, island, or another event or " +
			"work?",
		Criteria: map[string]string{
			"true": "On its own, player_answer is the ordinary name of something other " +
				"than correct_answer, e.g. 'Panama' (a country) for 'The Panama Canal', " +
				"or 'Hydrogen' (a gas) for 'The hydrogen bomb'.",
			"false": "On its own, player_answer still refers to correct_answer itself, e.g. " +
				"a surname such as 'Hemingway' for 'Ernest Hemingway', a shortened " +
				"title, an abbreviation, or a number written differently.",
		},
	},
	"too_vague": {
		Type: "noul",
		Instructions: "Compare player_answer with correct_answer. Did the player keep only a " +
			"generic or common part of correct_answer -- a common first name, or a " +
			"general word -- and leave out the part that actually identifies it?",
		Criteria: map[string]string{
			"true": "player_answer is only the generic part: 'Bill' for 'Buffalo Bill', " +
				"'Bridge' for 'The Golden Gate Bridge', 'Telescope' for 'The Hubble " +
				"Space Telescope', 'Apollo' for 'Apollo 13'.",
			"false": "player_answer keeps the identifying part, even if it drops a generic " +
				"word: 'Hemingway' for 'Ernest Hemingway', 'Golden Gate' for 'The " +
				"Golden Gate Bridge', 'Women's suffrage' for 'The women's suffrage " +
				"movement'; or it is a full alternate name rather than a fragment.",
		},
	},
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevState struct {
	Question        string   `json:"question"`
	CorrectAnswer   string   `json:"correct_answer"`
	AlreadyAccepted []string `json:"already_accepted"`
	WrongOptions    []string `json:"wrong_options"`
	PlayerAnswer    string   `json:"player_answer"`
}

type jevRequest struct {
	Model     string                 `json:"model"`
	State     jevState               `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Noul *float64 `json:"noul"`
	} `json:"answers"`
}

// Jev asks TypeSafe's Jev model. Like mail.Resend it is one JSON POST with a
// bearer token, so there is no SDK.
//
// It makes one attempt and does not retry. A player is waiting on the answer,
// and a retry after a 429 would spend the time the grader's own verdict could
// have been on screen; that verdict is the fallback anyway.
type Jev struct {
	APIKey string
	// Client is optional; nil means a client with a 5s timeout. The caller's
	// context is expected to carry the real, much shorter, deadline.
	Client *http.Client
	// Endpoint is optional; empty means the real API. Tests point it at an
	// httptest server.
	Endpoint string
}

func (j Jev) Score(ctx context.Context, q Question, typed string) (Scores, error) {
	if j.APIKey == "" {
		return Scores{}, fmt.Errorf("jev: no API key configured")
	}
	body, err := json.Marshal(jevRequest{
		Model: jevModel,
		State: jevState{
			Question:        q.Prompt,
			CorrectAnswer:   q.Answer,
			AlreadyAccepted: nonNil(q.Accepted),
			WrongOptions:    nonNil(q.WrongOptions),
			PlayerAnswer:    typed,
		},
		Questions: jevQuestions,
	})
	if err != nil {
		return Scores{}, fmt.Errorf("jev: encode request: %w", err)
	}

	endpoint := j.Endpoint
	if endpoint == "" {
		endpoint = jevEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Scores{}, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return Scores{}, fmt.Errorf("jev: send: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return Scores{}, fmt.Errorf("jev: status %d: %s", res.StatusCode, detail)
	}

	var parsed jevResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return Scores{}, fmt.Errorf("jev: decode response: %w", err)
	}
	// A missing score is an error, not a zero. Zero on NamesOtherThing or
	// TooVague means "certainly not", so defaulting would be the one way a
	// half-formed reply could turn into an acceptance.
	score := func(name string) (float64, error) {
		a, ok := parsed.Answers[name]
		if !ok || a.Noul == nil {
			return 0, fmt.Errorf("jev: response has no %q score", name)
		}
		return *a.Noul, nil
	}
	s := Scores{Model: parsed.Model}
	if s.Acceptable, err = score("acceptable"); err != nil {
		return Scores{}, err
	}
	if s.NamesOtherThing, err = score("names_other_thing"); err != nil {
		return Scores{}, err
	}
	if s.TooVague, err = score("too_vague"); err != nil {
		return Scores{}, err
	}
	return s, nil
}

// nonNil keeps an empty list as [] on the wire rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
