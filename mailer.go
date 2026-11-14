package main

// mailer.go — Gmail SMTP se OTP email bhejna (std library only).
// Env: GMAIL_USER (tumhara gmail) aur GMAIL_APP_PASSWORD (16 akshar ka Google App Password).

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Mailer interface {
	Enabled() bool
	Send(to, subject, text, htmlBody string) error
}

type nopMailer struct{}

func (nopMailer) Enabled() bool                { return false }
func (nopMailer) Send(_, _, _, _ string) error { return fmt.Errorf("mailer not configured") }

type GmailMailer struct {
	User     string
	Pass     string
	FromName string
	Host     string // test ke liye; default smtp.gmail.com
}

func NewGmailMailer(user, pass, fromName string) *GmailMailer {
	return &GmailMailer{
		User:     strings.TrimSpace(user),
		Pass:     strings.ReplaceAll(strings.TrimSpace(pass), " ", ""), // Google 4-4-4-4 spaces ke saath dikhata hai
		FromName: fromName,
		Host:     "smtp.gmail.com",
	}
}

func (g *GmailMailer) Enabled() bool { return g != nil && g.User != "" && g.Pass != "" }

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteString("\r\n")
		s = s[76:]
	}
	b.WriteString(s)
	return b.String()
}

func b64(s string) string { return wrap76(base64.StdEncoding.EncodeToString([]byte(s))) }

func (g *GmailMailer) build(to, subject, text, htmlBody string) []byte {
	rb := make([]byte, 8)
	rand.Read(rb)
	boundary := "nx" + hex.EncodeToString(rb)
	domain := "gmail.com"
	if i := strings.LastIndex(g.User, "@"); i >= 0 {
		domain = g.User[i+1:]
	}
	from := g.User
	if g.FromName != "" {
		from = mime.QEncoding.Encode("utf-8", g.FromName) + " <" + g.User + ">"
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(rb)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	b.WriteString("--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + b64(text) + "\r\n")
	b.WriteString("--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + b64(htmlBody) + "\r\n")
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// Send: pehle 465 (SSL), na chale toh 587 (STARTTLS).
func (g *GmailMailer) Send(to, subject, text, htmlBody string) error {
	if !g.Enabled() {
		return fmt.Errorf("gmail not configured")
	}
	if strings.ContainsAny(to, "\r\n") || strings.ContainsAny(subject, "\r\n") {
		return fmt.Errorf("bad header")
	}
	msg := g.build(to, subject, text, htmlBody)
	var errs []string
	for _, port := range []string{"465", "587"} {
		err := g.sendVia(port, to, msg)
		if err == nil {
			return nil
		}
		errs = append(errs, port+": "+err.Error())
	}
	return fmt.Errorf("smtp failed (%s)", strings.Join(errs, " | "))
}

func (g *GmailMailer) sendVia(port, to string, msg []byte) error {
	host := g.Host
	if host == "" {
		host = "smtp.gmail.com"
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	var c *smtp.Client
	if port == "465" {
		conn, err := tls.DialWithDialer(&d, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		conn.SetDeadline(time.Now().Add(25 * time.Second))
		if c, err = smtp.NewClient(conn, host); err != nil {
			conn.Close()
			return err
		}
	} else {
		conn, err := d.Dial("tcp", net.JoinHostPort(host, port))
		if err != nil {
			return err
		}
		conn.SetDeadline(time.Now().Add(25 * time.Second))
		if c, err = smtp.NewClient(conn, host); err != nil {
			conn.Close()
			return err
		}
		if err = c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			c.Close()
			return err
		}
	}
	defer c.Close()
	if err := c.Auth(smtp.PlainAuth("", g.User, g.Pass, host)); err != nil {
		return err
	}
	if err := c.Mail(g.User); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// otpMail: subject, plain text, html
func otpMail(site, purpose, code string) (string, string, string) {
	what := map[string]string{
		"signup": "account verify karne",
		"reset":  "password badalne",
		"email":  "naya email verify karne",
	}[purpose]
	if what == "" {
		what = "verify karne"
	}
	subject := fmt.Sprintf("%s ka OTP: %s", site, code)
	text := fmt.Sprintf("%s par %s ke liye aapka OTP: %s\n\nYe 10 minute tak chalega. Ye aapne nahi manga toh is email ko ignore kar dein. OTP kisi ko na batayein.\n", site, what, code)
	htm := fmt.Sprintf(`<div style="font-family:Arial,sans-serif;background:#0b0b0f;padding:24px">
<div style="max-width:420px;margin:auto;background:#16161d;border-radius:10px;padding:28px;color:#fff">
<h2 style="margin:0 0 6px;color:#e50914">%s</h2>
<p style="color:#bbb;margin:0 0 18px">%s ke liye aapka OTP:</p>
<div style="font-size:34px;letter-spacing:8px;font-weight:800;background:#0b0b0f;border-radius:8px;padding:14px;text-align:center">%s</div>
<p style="color:#999;font-size:13px;margin:18px 0 0">Ye 10 minute tak chalega. Aapne nahi manga toh is email ko ignore karein. OTP kisi ko na batayein.</p>
</div></div>`, html.EscapeString(site), html.EscapeString(strings.ToUpper(what[:1])+what[1:]), code)
	return subject, text, htm
}
