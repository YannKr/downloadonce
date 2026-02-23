package email

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
)

type Mailer struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func (m *Mailer) Enabled() bool {
	return m.Host != ""
}

func (m *Mailer) SendDownloadLink(to, recipientName, campaignName, downloadURL string) error {
	subject := fmt.Sprintf("Your download link for %s", campaignName)

	textBody := fmt.Sprintf(`Hello %s,

Your file "%s" is ready for download.

Download link: %s

This file has been prepared specifically for you and contains a digital fingerprint that uniquely identifies your copy. Unauthorized redistribution may allow the source to be traced.

If you did not expect this email, please disregard it.
`, recipientName, campaignName, downloadURL)

	htmlBody := fmt.Sprintf(`<html><body>
<p>Hello %s,</p>
<p>Your file "<strong>%s</strong>" is ready for download.</p>
<p><a href="%s" style="display:inline-block;padding:10px 24px;background:#4361ee;color:#fff;text-decoration:none;border-radius:4px;">Download File</a></p>
<p style="color:#666;font-size:12px;">This file has been prepared specifically for you and contains a digital fingerprint that uniquely identifies your copy. Unauthorized redistribution may allow the source to be traced.</p>
</body></html>`, recipientName, campaignName, downloadURL)

	return m.sendMultipart(to, subject, textBody, htmlBody)
}

func (m *Mailer) SendCampaignReady(to, ownerName, campaignName string, recipientCount int) error {
	subject := fmt.Sprintf("Campaign ready: %s", campaignName)

	textBody := fmt.Sprintf(`Hello %s,

Your campaign "%s" is ready. All %d watermarked copies have been generated.

Recipients can now download their files using their unique download links.
`, ownerName, campaignName, recipientCount)

	htmlBody := fmt.Sprintf(`<html><body>
<p>Hello %s,</p>
<p>Your campaign "<strong>%s</strong>" is ready. All <strong>%d</strong> watermarked copies have been generated.</p>
<p>Recipients can now download their files using their unique download links.</p>
</body></html>`, ownerName, campaignName, recipientCount)

	return m.sendMultipart(to, subject, textBody, htmlBody)
}

func (m *Mailer) sendMultipart(to, subject, textBody, htmlBody string) error {
	if !m.Enabled() {
		return nil
	}

	boundary := "----=_Part_downloadonce_boundary"

	headers := []string{
		fmt.Sprintf("From: %s", m.From),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		fmt.Sprintf(`Content-Type: multipart/alternative; boundary="%s"`, boundary),
	}

	body := strings.Join(headers, "\r\n") + "\r\n\r\n"
	body += "--" + boundary + "\r\n"
	body += "Content-Type: text/plain; charset=utf-8\r\n\r\n"
	body += textBody + "\r\n"
	body += "--" + boundary + "\r\n"
	body += "Content-Type: text/html; charset=utf-8\r\n\r\n"
	body += htmlBody + "\r\n"
	body += "--" + boundary + "--\r\n"

	addr := fmt.Sprintf("%s:%d", m.Host, m.Port)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	// STARTTLS
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{ServerName: m.Host}
		if err := client.StartTLS(tlsConfig); err != nil {
			slog.Warn("smtp starttls failed, continuing without", "error", err)
		}
	}

	// Auth
	if m.User != "" {
		auth := smtp.PlainAuth("", m.User, m.Pass, m.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(m.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}

	return client.Quit()
}
