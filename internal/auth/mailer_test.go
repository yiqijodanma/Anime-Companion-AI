package auth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func startSMTPCapture(t *testing.T) (string, <-chan []byte) {
	addr, messages := startSMTPCaptureServer(t, nil)
	return addr, messages
}

func startSMTPTLSCapture(t *testing.T) (string, <-chan []byte, *tls.Config) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	certificateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(certificate)

	addr, messages := startSMTPCaptureServer(t, &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: privateKey}},
	})
	return addr, messages, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: "localhost",
		RootCAs:    roots,
	}
}

func startSMTPCaptureServer(t *testing.T, serverTLSConfig *tls.Config) (string, <-chan []byte) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	messages := make(chan []byte, 1)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		rawConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		conn := rawConn
		if serverTLSConfig != nil {
			tlsConn := tls.Server(rawConn, serverTLSConfig)
			if handshakeErr := tlsConn.Handshake(); handshakeErr != nil {
				_ = rawConn.Close()
				return
			}
			conn = tlsConn
		}
		defer func() { _ = conn.Close() }()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		reply := func(value string) { _, _ = rw.WriteString(value); _ = rw.Flush() }
		reply("220 localhost ESMTP\r\n")
		var message bytes.Buffer
		inData := false
		for {
			line, readErr := rw.ReadString('\n')
			if readErr != nil {
				return
			}
			command := strings.TrimRight(line, "\r\n")
			if inData {
				if command == "." {
					messages <- append([]byte(nil), message.Bytes()...)
					inData = false
					reply("250 queued\r\n")
					continue
				}
				message.WriteString(line)
				continue
			}
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				reply("250-localhost\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(command, "AUTH PLAIN"):
				reply("235 authenticated\r\n")
			case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				reply("250 ok\r\n")
			case command == "DATA":
				inData = true
				reply("354 end with dot\r\n")
			case command == "QUIT":
				reply("221 bye\r\n")
				return
			default:
				reply("250 ok\r\n")
			}
		}
	}()
	return listener.Addr().String(), messages
}

func readAlternative(t *testing.T, raw []byte) (string, string, string) {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	require.NoError(t, err)
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", mediaType)
	reader := multipart.NewReader(message.Body, params["boundary"])
	var plain, html string
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		body, readErr := io.ReadAll(part)
		require.NoError(t, readErr)
		partType, _, parseErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		require.NoError(t, parseErr)
		if partType == "text/plain" {
			plain = string(body)
		}
		if partType == "text/html" {
			html = string(body)
		}
	}
	return subject, plain, html
}

func TestSMTPMailerSendsBrandedRegistrationAlternative(t *testing.T) {
	addr, messages := startSMTPCapture(t)
	mailer := SMTPMailer{Addr: addr, Host: "127.0.0.1", From: "SOS 团 <sender@example.com>"}
	require.NoError(t, mailer.SendVerification(context.Background(), "member@example.com", "123456", "register"))

	select {
	case raw := <-messages:
		subject, plain, html := readAlternative(t, raw)
		require.Equal(t, "SOS 团注册验证码", subject)
		for _, content := range []string{plain, html} {
			require.Contains(t, content, "123456")
			require.Contains(t, content, "10 分钟")
			require.Contains(t, content, "https://animecompanion.icu")
			require.Contains(t, content, "若非本人操作")
		}
		require.Contains(t, html, "SOS 团")
		require.NotContains(t, html, "<img")
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP message was not captured")
	}
}

func TestSMTPMailerSendsDistinctPasswordResetAlternative(t *testing.T) {
	addr, messages := startSMTPCapture(t)
	mailer := SMTPMailer{Addr: addr, Host: "127.0.0.1", From: "SOS 团 <sender@example.com>"}
	require.NoError(t, mailer.SendVerification(context.Background(), "member@example.com", "654321", "reset"))

	select {
	case raw := <-messages:
		subject, plain, html := readAlternative(t, raw)
		require.Equal(t, "SOS 团密码重置验证码", subject)
		for _, content := range []string{plain, html} {
			require.Contains(t, content, "654321")
			require.Contains(t, content, "密码重置")
			require.Contains(t, content, "10 分钟")
			require.Contains(t, content, "https://animecompanion.icu")
			require.Contains(t, content, "若非本人操作")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP message was not captured")
	}
}

func TestSMTPMailerSendsThroughImplicitTLS(t *testing.T) {
	addr, messages, clientTLSConfig := startSMTPTLSCapture(t)
	mailer := SMTPMailer{
		Addr:        addr,
		Host:        "localhost",
		Username:    "sender@example.com",
		Password:    "smtp-password",
		From:        "SOS 团 <sender@example.com>",
		ImplicitTLS: true,
		tlsConfig:   clientTLSConfig,
	}
	require.NoError(t, mailer.SendVerification(context.Background(), "member@example.com", "123456", "register"))

	select {
	case raw := <-messages:
		subject, plain, html := readAlternative(t, raw)
		require.Equal(t, "SOS 团注册验证码", subject)
		require.Contains(t, plain, "123456")
		require.Contains(t, html, "123456")
	case <-time.After(2 * time.Second):
		t.Fatal("implicit TLS SMTP message was not captured")
	}
}
