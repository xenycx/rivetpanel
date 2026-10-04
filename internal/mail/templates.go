package mail

import (
	"html"
	"strconv"
	"strings"
)

// Body is the content of a message before it is addressed.
type Body struct {
	Subject, Text, HTML string
}

// compose builds a plain-text body and a matching minimal HTML body from
// paragraphs and an optional call-to-action link. Every dynamic value is
// escaped for HTML; the text part is sent as is.
func compose(subject, heading string, paragraphs []string, linkLabel, link, footer string) Body {
	var t strings.Builder
	t.WriteString(heading + "\n\n")
	for _, p := range paragraphs {
		t.WriteString(p + "\n\n")
	}
	if link != "" {
		t.WriteString(link + "\n\n")
	}
	if footer != "" {
		t.WriteString(footer + "\n\n")
	}
	t.WriteString("RivetPanel")

	var h strings.Builder
	h.WriteString(`<!doctype html><html><body style="margin:0;background:#f4f5f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1c2330">`)
	h.WriteString(`<div style="max-width:520px;margin:0 auto;padding:24px 16px"><div style="background:#fff;border:1px solid #e3e6ec;border-radius:10px;padding:24px">`)
	h.WriteString(`<h1 style="margin:0 0 16px;font-size:18px">` + html.EscapeString(heading) + `</h1>`)
	for _, p := range paragraphs {
		h.WriteString(`<p style="margin:0 0 14px;font-size:14px;line-height:1.5">` + html.EscapeString(p) + `</p>`)
	}
	if link != "" {
		h.WriteString(`<p style="margin:20px 0"><a href="` + html.EscapeString(link) + `" style="background:#2f5bea;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;font-size:14px;display:inline-block">` + html.EscapeString(linkLabel) + `</a></p>`)
		h.WriteString(`<p style="margin:0 0 14px;font-size:12px;color:#6a7384;word-break:break-all">` + html.EscapeString(link) + `</p>`)
	}
	if footer != "" {
		h.WriteString(`<p style="margin:16px 0 0;font-size:12px;color:#6a7384;line-height:1.5">` + html.EscapeString(footer) + `</p>`)
	}
	h.WriteString(`</div><p style="text-align:center;font-size:12px;color:#9aa1af">RivetPanel</p></div></body></html>`)
	return Body{Subject: subject, Text: t.String(), HTML: h.String()}
}

// PasswordReset is sent when someone asks to reset a password.
func PasswordReset(link string, ttlMinutes int) Body {
	return compose("Reset your RivetPanel password", "Reset your password",
		[]string{
			"Someone asked to reset the password for this account. Use the button below to choose a new one.",
			"The link works once and expires in " + strconv.Itoa(ttlMinutes) + " minutes. Resetting signs the account out everywhere.",
		}, "Choose a new password", link,
		"If you did not ask for this, ignore this email; your password has not changed.")
}

// VerifyEmail asks the owner of an address to confirm it. change is true
// when the address is replacing the account's current one.
func VerifyEmail(link string, ttlHours int, change bool) Body {
	first := "Confirm that this email address belongs to you by using the button below."
	if change {
		first = "Someone asked to use this email address for a RivetPanel account. Use the button below to confirm it; the account's address changes only when you do."
	}
	return compose("Confirm your RivetPanel email address", "Confirm your email address",
		[]string{first, "The link works once and expires in " + strconv.Itoa(ttlHours) + " hours."},
		"Confirm email address", link,
		"If you did not ask for this, ignore this email; nothing changes.")
}

// Invitation is sent to the address an administrator invited.
func Invitation(link, role string, days int) Body {
	return compose("You are invited to RivetPanel", "You have been invited",
		[]string{
			"An administrator invited you to create a " + role + " account on a RivetPanel panel.",
			"The link is for one account and expires in " + strconv.Itoa(days) + " days.",
		}, "Create your account", link,
		"If you were not expecting this, you can ignore the email.")
}

// SecurityNotice tells a user their account security changed.
func SecurityNotice(what string) Body {
	return compose("RivetPanel security notice", "Your account security changed",
		[]string{what, "If this was not you, sign in and change your password now, or ask an administrator to reset two-step sign-in."},
		"", "", "")
}

// Alert carries a bot alert (crash, deploy, backup, health) to its owner.
func Alert(title, message string) Body {
	return compose(strip(title), strip(title), []string{message}, "", "", "You receive this because email alerts are on for your account; change it on your profile page.")
}

// Notification carries an in-panel notification (ticket reply, access
// change, node state) by email. link is an absolute panel URL or "".
func Notification(title, message, link string) Body {
	var paras []string
	if message != "" {
		paras = []string{message}
	}
	return compose(strip(title), strip(title), paras, "Open in RivetPanel", link,
		"You receive this because email is on for this kind of notification; change it in Settings → Notifications.")
}

// Test confirms that delivery works.
func Test() Body {
	return compose("RivetPanel test email", "Email delivery works",
		[]string{"This message was sent from the RivetPanel administration page to check your Mailgun settings."}, "", "", "")
}

// strip removes leading symbols and emoji from an alert title so subjects
// stay plain.
func strip(s string) string {
	s = strings.TrimSpace(s)
	for len(s) > 0 {
		r := []rune(s)[0]
		if r < 0x2000 && r != ' ' {
			break
		}
		s = strings.TrimSpace(string([]rune(s)[1:]))
	}
	if s == "" {
		return "RivetPanel alert"
	}
	return s
}
