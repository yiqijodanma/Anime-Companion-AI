package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"time"
)

const publicSiteURL = "https://animecompanion.icu"

type Mailer interface {
	SendVerification(ctx context.Context, to, code, purpose string) error
}

type SMTPMailer struct {
	Addr        string
	Host        string
	Username    string
	Password    string
	From        string
	ImplicitTLS bool
	tlsConfig   *tls.Config
}

type verificationEmail struct {
	subject string
	title   string
	intro   string
}

func emailForPurpose(purpose string) verificationEmail {
	if purpose == "reset" {
		return verificationEmail{
			subject: "SOS 团密码重置验证码",
			title:   "重置你的账号密码",
			intro:   "我们收到了你的密码重置请求。请使用下面的验证码继续操作。",
		}
	}
	return verificationEmail{
		subject: "SOS 团注册验证码",
		title:   "确认你的 SOS 团账号",
		intro:   "欢迎加入 SOS 团。请使用下面的验证码完成邮箱验证。",
	}
}

func composeVerificationMessage(from, to, code, purpose string) ([]byte, string, string, error) {
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse sender address: %w", err)
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse recipient address: %w", err)
	}
	template := emailForPurpose(purpose)
	plain := fmt.Sprintf("SOS 团\r\n\r\n%s\r\n\r\n%s\r\n\r\n验证码：%s\r\n有效期：10 分钟\r\n\r\n访问：%s\r\n\r\n若非本人操作，请忽略此邮件，你的账号不会受到影响。\r\n", template.title, template.intro, code, publicSiteURL)
	htmlBody := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<body style="margin:0;background:#f5f1e8;color:#302b2a;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif">
  <div style="max-width:560px;margin:0 auto;padding:32px 16px">
    <div style="background:#ffffff;border:1px solid #e4ddd2;border-radius:16px;overflow:hidden">
      <div style="background:#b5262d;color:#ffffff;padding:20px 28px;font-size:20px;font-weight:700;letter-spacing:1px">SOS 团</div>
      <div style="padding:28px">
        <h1 style="margin:0 0 14px;font-size:24px;line-height:1.35">%s</h1>
        <p style="margin:0 0 22px;line-height:1.7;color:#5f5752">%s</p>
        <div style="margin:0 0 12px;padding:18px;text-align:center;background:#fff7e8;border:1px solid #edcf9b;border-radius:12px;font-size:32px;font-weight:800;letter-spacing:8px;color:#9e2027">%s</div>
        <p style="margin:0 0 24px;text-align:center;color:#6d625d">验证码将在 <strong>10 分钟</strong>后失效</p>
        <p style="margin:0 0 10px;line-height:1.6"><a href="%s" style="color:#9e2027">访问 Anime Companion AI</a></p>
        <p style="margin:0;font-size:13px;line-height:1.6;color:#81756e">若非本人操作，请忽略此邮件，你的账号不会受到影响。</p>
      </div>
    </div>
  </div>
</body>
</html>`, html.EscapeString(template.title), html.EscapeString(template.intro), html.EscapeString(code), publicSiteURL)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	plainHeader := make(textproto.MIMEHeader)
	plainHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	plainHeader.Set("Content-Transfer-Encoding", "8bit")
	plainPart, err := writer.CreatePart(plainHeader)
	if err != nil {
		return nil, "", "", fmt.Errorf("create plain email part: %w", err)
	}
	if _, err := plainPart.Write([]byte(plain)); err != nil {
		return nil, "", "", fmt.Errorf("write plain email part: %w", err)
	}
	htmlHeader := make(textproto.MIMEHeader)
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "8bit")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return nil, "", "", fmt.Errorf("create HTML email part: %w", err)
	}
	if _, err := htmlPart.Write([]byte(htmlBody)); err != nil {
		return nil, "", "", fmt.Errorf("write HTML email part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", "", fmt.Errorf("finish email body: %w", err)
	}

	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", sender.String())
	fmt.Fprintf(&message, "To: %s\r\n", recipient.String())
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", template.subject))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	message.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
	message.Write(body.Bytes())
	return message.Bytes(), sender.Address, recipient.Address, nil
}

func (m SMTPMailer) SendVerification(ctx context.Context, to, code, purpose string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message, envelopeFrom, envelopeTo, err := composeVerificationMessage(m.From, to, code, purpose)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if m.Username != "" {
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}
	if m.ImplicitTLS {
		err = m.sendMailImplicitTLS(ctx, auth, envelopeFrom, envelopeTo, message)
	} else {
		err = smtp.SendMail(m.Addr, auth, envelopeFrom, []string{envelopeTo}, message)
	}
	if err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}

func (m SMTPMailer) sendMailImplicitTLS(ctx context.Context, auth smtp.Auth, from, to string, message []byte) error {
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", m.Addr)
	if err != nil {
		return err
	}
	defer rawConn.Close()

	tlsConfig := m.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: m.Host,
		}
	} else {
		tlsConfig = tlsConfig.Clone()
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = m.Host
		}
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
	}
	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return err
	}

	cancelWatchDone := make(chan struct{})
	defer close(cancelWatchDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = tlsConn.SetDeadline(time.Now())
		case <-cancelWatchDone:
		}
	}()

	client, err := smtp.NewClient(tlsConn, m.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
