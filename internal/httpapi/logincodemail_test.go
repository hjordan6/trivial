package httpapi

import (
	"strings"
	"testing"
)

// The code has to be in all four places a player might read it: the subject
// line, the preview text an inbox list shows, the HTML body, and the text
// fallback. Missing any one of them is a player who cannot sign in from
// wherever they happened to be looking.
func TestLoginCodeMessageCarriesTheCodeEverywhere(t *testing.T) {
	m := loginCodeMessage("player@example.test", "048221")

	if m.To != "player@example.test" {
		t.Errorf("to = %q", m.To)
	}
	if m.Subject != "Your Trivial sign-in code: 048221" {
		t.Errorf("subject = %q", m.Subject)
	}
	if !strings.Contains(m.Text, "048221") {
		t.Errorf("text = %q, want it to contain the code", m.Text)
	}
	if strings.Count(m.HTML, "048221") < 2 {
		t.Errorf("html should carry the code in both the preview line and the body, got %q", m.HTML)
	}
}

// A text part is what a plain-text client renders and what keeps a spam score
// down, so HTML must never be the only body.
func TestLoginCodeMessageAlwaysHasATextPart(t *testing.T) {
	m := loginCodeMessage("player@example.test", "048221")
	if strings.TrimSpace(m.Text) == "" {
		t.Fatal("text part is empty")
	}
	if m.HTML == "" {
		t.Fatal("html part is empty")
	}
}

// Both bodies tell the player where to look when nothing arrives. This is the
// most common failure of the whole sign-in flow and the hint is load-bearing,
// not decoration.
func TestLoginCodeMessageMentionsSpamInBothBodies(t *testing.T) {
	m := loginCodeMessage("player@example.test", "048221")
	if !strings.Contains(strings.ToLower(m.Text), "spam") {
		t.Errorf("text = %q, want a spam-folder hint", m.Text)
	}
	if !strings.Contains(strings.ToLower(m.HTML), "spam") {
		t.Error("html is missing a spam-folder hint")
	}
}

// Mail clients that drop <style> blocks are the majority, so every rule has to
// ride on the element it styles. A stylesheet creeping in would render as
// unstyled text for those clients without failing anywhere a developer looks.
func TestLoginCodeHTMLKeepsItsStylesInline(t *testing.T) {
	m := loginCodeMessage("player@example.test", "048221")
	if strings.Contains(m.HTML, "<style") || strings.Contains(m.HTML, "<link") {
		t.Error("html carries a stylesheet, which mobile Gmail strips")
	}
	if !strings.Contains(m.HTML, "font-size:44px") {
		t.Error("the code is no longer rendered large")
	}
	if !strings.Contains(m.HTML, "font-weight:700;letter-spacing:.2em") {
		t.Error("the code is no longer rendered bold")
	}
}
