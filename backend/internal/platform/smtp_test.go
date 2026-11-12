package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSMTPTestValidation(t *testing.T) {
	base := smtpTestRequest{Host: "smtp.example.com", Port: 587, From: "sender@example.com", To: "recipient@example.com", TLS: true}
	if !base.valid() {
		t.Fatal("valid settings rejected")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "smtp.example.com."} {
		in := base
		in.Host = host
		if !in.valid() {
			t.Fatalf("valid host rejected: %s", host)
		}
	}
	cases := map[string]func(*smtpTestRequest){
		"empty host":          func(in *smtpTestRequest) { in.Host = "" },
		"host injection":      func(in *smtpTestRequest) { in.Host = "smtp.example.com\r\nMAIL FROM:x" },
		"host URL":            func(in *smtpTestRequest) { in.Host = "https://smtp.example.com" },
		"host with port":      func(in *smtpTestRequest) { in.Host = "smtp.example.com:587" },
		"host whitespace":     func(in *smtpTestRequest) { in.Host = " smtp.example.com" },
		"invalid label":       func(in *smtpTestRequest) { in.Host = "-smtp.example.com" },
		"zero port":           func(in *smtpTestRequest) { in.Port = 0 },
		"large port":          func(in *smtpTestRequest) { in.Port = 65536 },
		"sender name":         func(in *smtpTestRequest) { in.From = "Sender <sender@example.com>" },
		"sender brackets":     func(in *smtpTestRequest) { in.From = "<sender@example.com>" },
		"multiple recipients": func(in *smtpTestRequest) { in.To = "a@example.com,b@example.com" },
		"recipient injection": func(in *smtpTestRequest) { in.To = "a@example.com\r\nBcc: b@example.com" },
		"recipient comment":   func(in *smtpTestRequest) { in.To = "recipient@example.com (comment)" },
		"empty recipient":     func(in *smtpTestRequest) { in.To = "" },
		"masked password":     func(in *smtpTestRequest) { in.Password = "********" },
		"large password":      func(in *smtpTestRequest) { in.Password = strings.Repeat("s", 4097) },
		"username injection":  func(in *smtpTestRequest) { in.Username = "u\r\nAUTH" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := base
			change(&in)
			if in.valid() {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestSMTPTestAuthorizationAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &App{}
	for _, tc := range []struct {
		name          string
		admin, apiKey bool
		body          string
		status        int
	}{
		{"non admin", false, false, `{}`, 403},
		{"API key admin", true, true, `{}`, 403},
		{"invalid JSON", true, false, `{`, 400},
		{"missing fields", true, false, `{}`, 400},
		{"wrong type", true, false, `{"port":"587"}`, 400},
		{"unknown field", true, false, `{"secret":"hidden"}`, 400},
		{"multiple objects", true, false, `{} {}`, 400},
		{"oversize", true, false, `{"host":"` + strings.Repeat("a", 17<<10) + `"}`, 400},
		{"plaintext auth", true, false, `{"host":"localhost","port":25,"from":"a@example.com","to":"b@example.com","username":"user","password":"do-not-echo"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("user", user{IsPlatformAdmin: tc.admin})
			if tc.apiKey {
				c.Set("api_key", iamAuthenticatedKey{})
			}
			c.Request = httptest.NewRequest("POST", "/api/v1/settings/smtp-test", strings.NewReader(tc.body))
			a.smtpTest(c)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if strings.Contains(w.Body.String(), "do-not-echo") {
				t.Fatal("password echoed")
			}
		})
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/settings/smtp-test", strings.NewReader(`{}`)))
	if w.Code != 401 {
		t.Fatalf("unguarded SMTP route: %d", w.Code)
	}
}

func TestSMTPTransport(t *testing.T) {
	for _, mode := range []string{"accepted", "require STARTTLS", "no plaintext auth", "recipient rejected"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			result := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					result <- "accept failed"
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				_, _ = io.WriteString(conn, "220 local SMTP\r\n")
				var received strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						result <- received.String()
						return
					}
					received.WriteString(line)
					switch {
					case strings.HasPrefix(line, "EHLO"):
						_, _ = io.WriteString(conn, "250-local SMTP\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "MAIL FROM:"):
						_, _ = io.WriteString(conn, "250 sender OK\r\n")
					case strings.HasPrefix(line, "RCPT TO:"):
						if mode == "recipient rejected" {
							_, _ = io.WriteString(conn, "550 do-not-echo remote-secret\r\n")
						} else {
							_, _ = io.WriteString(conn, "250 recipient OK\r\n")
						}
					case line == "DATA\r\n":
						_, _ = io.WriteString(conn, "354 send message\r\n")
						for {
							data, err := reader.ReadString('\n')
							if err != nil {
								result <- "data failed"
								return
							}
							received.WriteString(data)
							if data == ".\r\n" {
								break
							}
						}
						_, _ = io.WriteString(conn, "250 queued\r\n")
					default:
						_, _ = io.WriteString(conn, "500 unsupported\r\n")
					}
				}
			}()
			port := listener.Addr().(*net.TCPAddr).Port
			in := smtpTestRequest{Host: "127.0.0.1", Port: port, From: "a@example.com", To: "b@example.com"}
			if mode == "require STARTTLS" {
				in.TLS = true
			}
			if mode == "no plaintext auth" {
				in.Username, in.Password = "user", "do-not-echo"
			}
			err = sendSMTPTest(context.Background(), in)
			if (err == nil) != (mode == "accepted") {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), "do-not-echo") || strings.Contains(err.Error(), "remote-secret")) {
				t.Fatal("remote error leaked")
			}
			transcript := <-result
			if strings.Contains(transcript, "AUTH PLAIN ") {
				t.Fatal("sent plaintext authentication")
			}
			if mode == "accepted" && !strings.Contains(transcript, "Subject: [TEST] SMTP configuration test") {
				t.Fatal("test email missing")
			}
			if mode == "require STARTTLS" && strings.Contains(transcript, "MAIL FROM") {
				t.Fatal("STARTTLS downgrade allowed")
			}
		})
	}
}

func TestSMTPCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Read(make([]byte, 1))
		close(closed)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- sendSMTPTest(ctx, smtpTestRequest{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port})
	}()
	<-accepted
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled SMTP succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt greeting")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection not closed")
	}
}

func TestSMTPTestFailureResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(conn, "550 remote-secret\r\n")
	}()
	in := smtpTestRequest{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, TLS: true, From: "a@example.com", To: "b@example.com", Username: "u", Password: "do-not-echo"}
	data, _ := json.Marshal(in)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user{IsPlatformAdmin: true})
	c.Request = httptest.NewRequest("POST", "/api/v1/settings/smtp-test", strings.NewReader(string(data)))
	(&App{}).smtpTest(c)
	if w.Code != 502 || strings.Contains(w.Body.String(), "remote-secret") || strings.Contains(w.Body.String(), "do-not-echo") {
		t.Fatalf("unsafe failure response: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), strconv.Itoa(502)) {
		t.Fatal("error envelope missing")
	}
}
