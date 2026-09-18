package httpapi

import (
	"html/template"
	"strings"

	"github.com/hjordan6/trivial/internal/mail"
)

// loginCodeMessage builds the sign-in email.
//
// The code goes in the subject as well as both bodies, so it is readable from a
// notification without opening the mail. That is the whole payoff of choosing a
// code over a link: the player never leaves their tab.
func loginCodeMessage(email, code string) mail.Message {
	var html strings.Builder
	// The only input is six digits minted by accounts.RequestCode, so there is
	// nothing here an attacker can steer. html/template is used anyway because
	// the alternative -- a fmt.Sprintf that is safe only as long as nobody ever
	// puts a player-supplied string in this mail -- is the kind of safety that
	// quietly stops being true.
	if err := loginCodeHTML.Execute(&html, struct{ Code string }{code}); err != nil {
		// A template that cannot render its own literal is a programming error,
		// not a runtime condition. Falling back to text alone still delivers a
		// working code, which is the one thing this email owes the player.
		return mail.Message{To: email, Subject: loginCodeSubject(code), Text: loginCodeText(code)}
	}
	return mail.Message{
		To:      email,
		Subject: loginCodeSubject(code),
		Text:    loginCodeText(code),
		HTML:    html.String(),
	}
}

func loginCodeSubject(code string) string {
	return "Your Trivial sign-in code: " + code
}

// loginCodeText is the fallback body, and the one the development sender logs.
func loginCodeText(code string) string {
	return code + " is your Trivial sign-in code.\n\n" +
		"It works once, and only in the tab you started in. If you asked for\n" +
		"more than one code, only the newest one works.\n\n" +
		"Not in your inbox? Check your spam folder -- our sending domain is new,\n" +
		"so codes often land there. Marking this message \"not spam\" keeps the\n" +
		"next one out of it.\n\n" +
		"If you did not ask for this, you can ignore this email."
}

// loginCodeHTML is the designed half of the sign-in email.
//
// Nested tables and inline styles, which is not how anything else in this
// project is written and is not a matter of taste: Gmail drops <style> blocks
// on its mobile clients and Outlook still renders through Word, so a stylesheet
// and a flex container would arrive as unstyled text for a large share of
// players. Every rule that matters is therefore on the element it styles, and
// the palette is the site's own -- ink, paper, acid -- typed out in hex because
// a custom property is another thing mail clients drop.
//
// The one job is the code: large, bold, monospace, and reachable in one glance
// from a notification shade.
var loginCodeHTML = template.Must(template.New("loginCode").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light">
<title>Your Trivial sign-in code</title>
</head>
<body style="margin:0;padding:0;background:#f4f1e8;">
<!-- The preview line, which is what an inbox list shows next to the subject.
     Hidden in the body itself so it is not said twice. -->
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Code}} is your sign-in code. It works once.</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f4f1e8;">
<tr><td align="center" style="padding:32px 12px;">
<table role="presentation" width="460" cellpadding="0" cellspacing="0" border="0" style="width:460px;max-width:100%;background:#fffdf7;border:1px solid #cbc7bc;border-radius:6px;">

<tr><td style="padding:30px 30px 0;font-family:'Courier New',Courier,monospace;font-size:11px;font-weight:500;letter-spacing:.16em;text-transform:uppercase;color:#6a6b65;">Trivial &middot; daily trivia</td></tr>

<tr><td style="padding:10px 30px 0;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:25px;font-weight:700;letter-spacing:-.02em;line-height:1.2;color:#1b1d1b;">Your sign-in code</td></tr>

<!-- text-indent cancels the trailing letter-spacing on the last digit, which
     would otherwise push the whole code a third of a character left of centre. -->
<tr><td style="padding:24px 30px 0;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
    <tr><td align="center" style="background:#d9ff55;border:2px solid #1b1d1b;border-radius:6px;padding:22px 10px;font-family:'Courier New',Courier,monospace;font-size:44px;line-height:1.1;font-weight:700;letter-spacing:.2em;text-indent:.2em;color:#1b1d1b;">{{.Code}}</td></tr>
  </table>
</td></tr>

<tr><td style="padding:22px 30px 0;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:15px;line-height:1.6;color:#55564f;">
  Type it into the tab you started in. It works once, and only there &mdash; and if you asked for more than one code, only the newest one works.
</td></tr>

<tr><td style="padding:20px 30px 26px;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#fdeceb;border:1px solid #ff6846;border-radius:4px;">
    <tr><td style="padding:14px 16px;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:14px;line-height:1.55;color:#8d2c19;">
      <strong style="display:block;font-size:15px;margin-bottom:4px;color:#8d2c19;">Found this in spam?</strong>
      Mark it &ldquo;not spam&rdquo; and the next one will land in your inbox. Our sending domain is new, so filters are still making their minds up about us.
    </td></tr>
  </table>
</td></tr>

<tr><td style="padding:22px 30px 30px;border-top:1px solid #cbc7bc;font-family:system-ui,-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:13px;line-height:1.55;color:#6a6b65;">
  Didn&rsquo;t ask for this? Someone typed your address into the sign-in form. Ignore this email and nothing happens &mdash; the code expires on its own and no account is created.
</td></tr>

</table>
</td></tr>
</table>
</body>
</html>
`))
