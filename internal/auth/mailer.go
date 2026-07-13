package auth

import (
	"context"
	"fmt"
	"net/smtp"
)

type Mailer interface {
	SendVerification(ctx context.Context, to, code, purpose string) error
}

type SMTPMailer struct {
	Addr     string
	Host     string
	Username string
	Password string
	From     string
}

func (m SMTPMailer) SendVerification(ctx context.Context, to, code, purpose string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	subject := "SOS 团入团验证码"
	body := "你的入团验证码是：" + code + "\r\n\r\n10 分钟内有效。若非本人操作，请忽略此邮件。"
	if purpose == "reset" {
		subject = "SOS 团密码重置验证码"
		body = "你的密码重置验证码是：" + code + "\r\n\r\n10 分钟内有效。若非本人操作，请忽略此邮件。"
	}
	message := []byte("From: " + m.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	var auth smtp.Auth
	if m.Username != "" {
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}
	if err := smtp.SendMail(m.Addr, auth, m.From, []string{to}, message); err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}
