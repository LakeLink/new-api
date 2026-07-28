package common

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/mail"
	"net/smtp"
	"slices"
	"strings"
	"time"
)

func generateMessageID(from string) (string, error) {
	at := strings.LastIndexByte(from, '@')
	if at <= 0 || at == len(from)-1 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := from[at+1:]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

func newSMTPClient(addr string) (*smtp.Client, error) {
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.Dial("tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

func SendEmail(subject string, receiver string, content string) error {
	from := strings.TrimSpace(SMTPFrom)
	if from == "" { // for compatibility
		from = strings.TrimSpace(SMTPAccount)
	}
	fromAddress, err := mail.ParseAddress(from)
	if err != nil || fromAddress.Address != from {
		return fmt.Errorf("invalid SMTP sender address")
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return fmt.Errorf("SMTP 服务器未配置")
	}
	rawRecipients := strings.Split(receiver, ";")
	recipients := make([]string, 0, len(rawRecipients))
	toHeaders := make([]string, 0, len(rawRecipients))
	for _, rawRecipient := range rawRecipients {
		rawRecipient = strings.TrimSpace(rawRecipient)
		recipientAddress, parseErr := mail.ParseAddress(rawRecipient)
		if parseErr != nil || recipientAddress.Address != rawRecipient {
			return fmt.Errorf("invalid SMTP recipient address")
		}
		recipients = append(recipients, recipientAddress.Address)
		toHeaders = append(toHeaders, recipientAddress.String())
	}
	if len(recipients) == 0 {
		return fmt.Errorf("SMTP recipient address is required")
	}
	id, err := generateMessageID(fromAddress.Address)
	if err != nil {
		return err
	}
	encodedSubject := fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(subject)))
	fromHeader := (&mail.Address{Name: SystemName, Address: fromAddress.Address}).String()
	mail := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+ // 添加 Message-ID 头
		"Content-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n",
		strings.Join(toHeaders, ", "), fromHeader, encodedSubject, time.Now().Format(time.RFC1123Z), id, content))
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(fromAddress.Address); err != nil {
		return err
	}
	for _, receiver := range recipients {
		if err = client.Rcpt(receiver); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(mail)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	err = client.Quit()
	if err != nil {
		SysError(fmt.Sprintf("failed to send email to %s: %v", MaskEmail(receiver), err))
	}
	return err
}
