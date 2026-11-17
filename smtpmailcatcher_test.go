package smtpmailcatcher_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Trysac/u920-smtp-mail-catcher"
)

func startTestServer(t *testing.T, cfg smtpmailcatcher.Config) (*smtpmailcatcher.Server, string, func()) {
	t.Helper()
	srv := smtpmailcatcher.NewServer(cfg)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := l.Addr().String()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(l)
	}()

	cleanup := func() {
		_ = srv.Close()
		<-errCh
	}
	return srv, addr, cleanup
}

// Acceptance Test 1:
// Oracle: net/smtp.SendMail to the server with from "sender@example.test", to "a@example.test"
// and "b@example.test" and a multipart/mixed body with text "Hello" and attachment "report.csv"
// (Content-Type "text/csv", base64 "aWQsdmFsdWUKMSwy") stores one Message with To ["a@example.test", "b@example.test"],
// Text "Hello" and one Attachment with Filename "report.csv", Size 12 and Data "id,value\n1,2"
func TestAcceptance1_OracleSendMailMultipart(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	from := "sender@example.test"
	to := []string{"a@example.test", "b@example.test"}
	body := strings.Join([]string{
		"From: sender@example.test",
		"To: a@example.test, b@example.test",
		"Subject: Test Multipart",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=frontier",
		"",
		"--frontier",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hello",
		"--frontier",
		"Content-Type: text/csv",
		"Content-Disposition: attachment; filename=\"report.csv\"",
		"Content-Transfer-Encoding: base64",
		"",
		"aWQsdmFsdWUKMSwy",
		"--frontier--",
		"",
	}, "\r\n")

	err := smtp.SendMail(addr, nil, from, to, []byte(body))
	if err != nil {
		t.Fatalf("SendMail failed: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.From != from {
		t.Errorf("expected From %q, got %q", from, msg.From)
	}
	if len(msg.To) != 2 || msg.To[0] != "a@example.test" || msg.To[1] != "b@example.test" {
		t.Errorf("unexpected To: %v", msg.To)
	}
	if strings.TrimSpace(msg.Text) != "Hello" {
		t.Errorf("expected Text 'Hello', got %q", msg.Text)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.Filename != "report.csv" {
		t.Errorf("expected attachment filename 'report.csv', got %q", att.Filename)
	}
	if att.Size != 12 {
		t.Errorf("expected size 12, got %d", att.Size)
	}
	if string(att.Data) != "id,value\n1,2" {
		t.Errorf("expected data 'id,value\n1,2', got %q", string(att.Data))
	}
}

// Acceptance Test 2:
// A multipart/alternative message keeps both the text and the HTML part, and the subject
// "=?UTF-8?B?w4lsw6g=?=" is decoded to "Élè"; a quoted-printable body "caf=C3=A9" is decoded to "café"
func TestAcceptance2_MIMEAlternativeAndDecoding(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	msgContent := strings.Join([]string{
		"From: <sender@example.test>",
		"To: <user@example.test>",
		"Subject: =?UTF-8?B?w4lsw6g=?=",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=alt_boundary",
		"",
		"--alt_boundary",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"caf=C3=A9",
		"--alt_boundary",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>HTML Body</p>",
		"--alt_boundary--",
		"",
	}, "\r\n")

	err := smtp.SendMail(addr, nil, "sender@example.test", []string{"user@example.test"}, []byte(msgContent))
	if err != nil {
		t.Fatalf("SendMail failed: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Subject != "Élè" {
		t.Errorf("expected Subject 'Élè', got %q", msg.Subject)
	}
	if strings.TrimSpace(msg.Text) != "café" {
		t.Errorf("expected Text 'café', got %q", msg.Text)
	}
	if msg.HTML != "<p>HTML Body</p>" {
		t.Errorf("expected HTML '<p>HTML Body</p>', got %q", msg.HTML)
	}
}

// Acceptance Test 3:
// A DATA line "..dot" is stored as ".dot" (RFC 5321 section 4.5.2) and the message ends only at a line consisting of "."
func TestAcceptance3_DotTransparency(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	c, err := textproto.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer c.Close()

	_, _, _ = c.ReadResponse(220)
	c.Cmd("HELO client.test")
	c.ReadResponse(250)
	c.Cmd("MAIL FROM:<alice@example.test>")
	c.ReadResponse(250)
	c.Cmd("RCPT TO:<bob@example.test>")
	c.ReadResponse(250)
	c.Cmd("DATA")
	c.ReadResponse(354)

	c.W.WriteString("Subject: Dot Test\r\n\r\n..dot\r\n..another dot\r\n.\r\n")
	c.W.Flush()
	c.ReadResponse(250)

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	rawStr := string(msgs[0].Raw)
	if !strings.Contains(rawStr, ".dot\r\n") {
		t.Errorf("expected Raw to contain '.dot\r\n', got: %q", rawStr)
	}
	if !strings.Contains(rawStr, ".another dot\r\n") {
		t.Errorf("expected Raw to contain '.another dot\r\n', got: %q", rawStr)
	}
}

// Acceptance Test 4:
// With MaxMessageBytes 10485760, "MAIL FROM:<x@example.test> SIZE=20000000" gets
// "552 5.3.4 Message size exceeds fixed limit" and DATA beyond the limit gets the same reply after the final "."
func TestAcceptance4_MaxMessageBytesLimit(t *testing.T) {
	_, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{
		MaxMessageBytes: 10485760,
	})
	defer cleanup()

	c, err := textproto.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer c.Close()

	_, _, _ = c.ReadResponse(220)
	c.Cmd("EHLO client.test")
	c.ReadResponse(250)

	// Test 1: SIZE parameter in MAIL FROM
	c.Cmd("MAIL FROM:<x@example.test> SIZE=20000000")
	code, msg, err := c.ReadResponse(552)
	if err != nil || code != 552 || !strings.Contains(msg, "Message size exceeds fixed limit") {
		t.Fatalf("expected 552 Message size exceeds fixed limit, got %d %q %v", code, msg, err)
	}

	// Test 2: DATA beyond limit (use small limit server for this check)
	smallSrv, smallAddr, smallCleanup := startTestServer(t, smtpmailcatcher.Config{
		MaxMessageBytes: 50,
	})
	defer smallCleanup()

	c2, err := textproto.Dial("tcp", smallAddr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer c2.Close()

	c2.ReadResponse(220)
	c2.Cmd("HELO client.test")
	c2.ReadResponse(250)
	c2.Cmd("MAIL FROM:<x@example.test>")
	c2.ReadResponse(250)
	c2.Cmd("RCPT TO:<y@example.test>")
	c2.ReadResponse(250)
	c2.Cmd("DATA")
	c2.ReadResponse(354)

	overflow := strings.Repeat("A", 100)
	c2.W.WriteString(overflow + "\r\n.\r\n")
	c2.W.Flush()
	code2, msg2, err2 := c2.ReadResponse(552)
	if err2 != nil || code2 != 552 || !strings.Contains(msg2, "Message size exceeds fixed limit") {
		t.Fatalf("expected 552 after final dot, got %d %q %v", code2, msg2, err2)
	}
	if len(smallSrv.Messages()) != 0 {
		t.Fatalf("expected no stored message, got %d", len(smallSrv.Messages()))
	}
}

// Acceptance Test 5:
// RCPT before MAIL gets "503 5.5.1 Bad sequence of commands"; a 1001-octet command line
// gets "500 5.5.2 Line too long"; the 101st RCPT gets "452 4.5.3 Too many recipients";
// an idle connection with CommandTimeout 20ms gets "421 4.4.2 Timeout" and is closed within 1 s
func TestAcceptance5_CommandErrorsAndTimeout(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{
		MaxRecipients:  100,
		CommandTimeout: 20 * time.Millisecond,
	})
	defer cleanup()

	c, err := textproto.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.Close()
	c.ReadResponse(220)
	c.Cmd("HELO test.local")
	c.ReadResponse(250)

	// RCPT before MAIL
	c.Cmd("RCPT TO:<bob@example.test>")
	code, msg, _ := c.ReadResponse(503)
	if code != 503 || !strings.Contains(msg, "Bad sequence of commands") {
		t.Errorf("expected 503 Bad sequence of commands, got %d %q", code, msg)
	}

	// 1001-octet command line
	longCmd := "NOOP " + strings.Repeat("A", 996)
	if len(longCmd)+2 <= 1000 {
		t.Fatalf("expected length > 1000")
	}
	c.Cmd(longCmd)
	code, msg, _ = c.ReadResponse(500)
	if code != 500 || !strings.Contains(msg, "Line too long") {
		t.Errorf("expected 500 Line too long, got %d %q", code, msg)
	}

	// 101st RCPT
	c.Cmd("MAIL FROM:<alice@example.test>")
	c.ReadResponse(250)
	for i := 0; i < 100; i++ {
		c.Cmd(fmt.Sprintf("RCPT TO:<rec%d@example.test>", i))
		c.ReadResponse(250)
	}
	c.Cmd("RCPT TO:<overflow@example.test>")
	code, msg, _ = c.ReadResponse(452)
	if code != 452 || !strings.Contains(msg, "Too many recipients") {
		t.Errorf("expected 452 Too many recipients, got %d %q", code, msg)
	}

	// Idle connection timeout with 20ms CommandTimeout
	cIdle, err := textproto.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("idle dial error: %v", err)
	}
	defer cIdle.Close()
	cIdle.ReadResponse(220)

	start := time.Now()
	code, msg, err = cIdle.ReadResponse(421)
	dur := time.Since(start)
	if dur > 1*time.Second {
		t.Errorf("expected timeout closure within 1 s, took %v", dur)
	}
	if code != 421 || !strings.Contains(msg, "Timeout") {
		t.Errorf("expected 421 Timeout, got %d %q (err: %v)", code, msg, err)
	}
	_ = srv
}

// Acceptance Test 6:
// GET /api/messages returns JSON newest first;
// GET /messages/{id}/html has header Content-Security-Policy "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox";
// the index page shows a subject "<script>" as "&lt;script&gt;";
// attachment downloads have X-Content-Type-Options "nosniff" and Content-Disposition with the filename stripped of quotes and control characters
func TestAcceptance6_HTTPAPIAndSecurityHeaders(t *testing.T) {
	srv := smtpmailcatcher.NewServer(smtpmailcatcher.Config{})
	handler := srv.HTTPHandler()

	// Populate server via SMTP listener
	srvL, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	_ = smtp.SendMail(addr, nil, "from1@test.com", []string{"to1@test.com"}, []byte("Subject: <script>\r\n\r\nBody 1"))

	msgAtt := strings.Join([]string{
		"From: from2@test.com",
		"To: to2@test.com",
		"Subject: Second Message",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=bound123",
		"",
		"--bound123",
		"Content-Type: text/html",
		"",
		"<h1>Safe HTML</h1>",
		"--bound123",
		"Content-Type: text/plain",
		"Content-Disposition: attachment; filename=\"safe\"name\x07.txt\"",
		"",
		"attachment content",
		"--bound123--",
		"",
	}, "\r\n")
	_ = smtp.SendMail(addr, nil, "from2@test.com", []string{"to2@test.com"}, []byte(msgAtt))

	httpSrv := httptest.NewServer(srvL.HTTPHandler())
	defer httpSrv.Close()

	// Check GET /api/messages returns JSON newest first
	res, err := http.Get(httpSrv.URL + "/api/messages")
	if err != nil {
		t.Fatalf("GET /api/messages failed: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET /api/messages status = %d", res.StatusCode)
	}
	var apiMsgs []smtpmailcatcher.Message
	_ = json.NewDecoder(res.Body).Decode(&apiMsgs)
	res.Body.Close()
	if len(apiMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(apiMsgs))
	}
	if apiMsgs[0].Subject != "Second Message" || apiMsgs[1].Subject != "<script>" {
		t.Errorf("messages not newest first: [0]=%q, [1]=%q", apiMsgs[0].Subject, apiMsgs[1].Subject)
	}

	secondID := apiMsgs[0].ID

	// Check GET /messages/{id}/html headers
	resHTML, err := http.Get(fmt.Sprintf("%s/messages/%s/html", httpSrv.URL, secondID))
	if err != nil {
		t.Fatalf("GET html failed: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resHTML.Body)
	resHTML.Body.Close()
	if gotCSP := resHTML.Header.Get("Content-Security-Policy"); gotCSP != "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox" {
		t.Errorf("unexpected CSP header: %q", gotCSP)
	}
	if gotNosniff := resHTML.Header.Get("X-Content-Type-Options"); gotNosniff != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", gotNosniff)
	}
	if string(bodyBytes) != "<h1>Safe HTML</h1>" {
		t.Errorf("expected body '<h1>Safe HTML</h1>', got %q", string(bodyBytes))
	}

	// Check index page shows <script> escaped as &lt;script&gt;
	resIndex, err := http.Get(httpSrv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	indexBody, _ := io.ReadAll(resIndex.Body)
	resIndex.Body.Close()
	if !strings.Contains(string(indexBody), "&lt;script&gt;") {
		t.Errorf("expected index body to contain '&lt;script&gt;', got %s", string(indexBody))
	}
	if strings.Contains(string(indexBody), "<script>") {
		t.Errorf("index body contains unescaped <script>")
	}

	// Check attachment download headers
	resAtt, err := http.Get(fmt.Sprintf("%s/api/messages/%s/attachments/0", httpSrv.URL, secondID))
	if err != nil {
		t.Fatalf("GET attachment failed: %v", err)
	}
	attBody, _ := io.ReadAll(resAtt.Body)
	resAtt.Body.Close()
	if gotNosniff := resAtt.Header.Get("X-Content-Type-Options"); gotNosniff != "nosniff" {
		t.Errorf("expected attachment X-Content-Type-Options 'nosniff', got %q", gotNosniff)
	}
	cd := resAtt.Header.Get("Content-Disposition")
	if strings.Contains(cd, "\"") || strings.Contains(cd, "\x07") {
		t.Errorf("Content-Disposition has unstripped quotes or control chars: %q", cd)
	}
	if !strings.Contains(cd, "filename=safename.txt") {
		t.Errorf("expected sanitized filename 'safename.txt', got %q", cd)
	}
	if string(attBody) != "attachment content" {
		t.Errorf("expected attachment content 'attachment content', got %q", string(attBody))
	}
	_ = handler
}

// Acceptance Test 7:
// With MaxMessages 2 a third message evicts the oldest; DELETE /api/messages empties the store and returns 204
func TestAcceptance7_EvictionAndStoreClear(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{
		MaxMessages: 2,
	})
	defer cleanup()

	for i := 1; i <= 3; i++ {
		err := smtp.SendMail(addr, nil, fmt.Sprintf("from%d@test.com", i), []string{"to@test.com"}, []byte(fmt.Sprintf("Subject: Msg %d\r\n\r\nBody", i)))
		if err != nil {
			t.Fatalf("failed to send message %d: %v", i, err)
		}
	}

	msgs := srv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages due to eviction, got %d", len(msgs))
	}
	if msgs[0].Subject != "Msg 3" || msgs[1].Subject != "Msg 2" {
		t.Errorf("expected [Msg 3, Msg 2], got [%s, %s]", msgs[0].Subject, msgs[1].Subject)
	}

	httpSrv := httptest.NewServer(srv.HTTPHandler())
	defer httpSrv.Close()

	req, err := http.NewRequest("DELETE", httpSrv.URL+"/api/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest DELETE: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/messages failed: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204 No Content, got %d", res.StatusCode)
	}
	if len(srv.Messages()) != 0 {
		t.Errorf("expected empty store after DELETE, got %d", len(srv.Messages()))
	}
}

// Security and Threat Model Tests:
// 1. Script execution from captured HTML (sandboxed iframe plus CSP)
func TestSecurity_ScriptExecutionInHTML(t *testing.T) {
	srv := smtpmailcatcher.NewServer(smtpmailcatcher.Config{})
	srvL, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	maliciousHTML := "<script>alert('pwned')</script><img src='x' onerror='alert(1)'>"
	msgPayload := strings.Join([]string{
		"From: attacker@evil.test",
		"To: victim@example.test",
		"Subject: Attack",
		"Content-Type: text/html",
		"",
		maliciousHTML,
	}, "\r\n")

	_ = smtp.SendMail(addr, nil, "attacker@evil.test", []string{"victim@example.test"}, []byte(msgPayload))

	httpSrv := httptest.NewServer(srvL.HTTPHandler())
	defer httpSrv.Close()

	msgs := srvL.Messages()
	if len(msgs) == 0 {
		t.Fatalf("expected message captured")
	}
	id := msgs[0].ID

	// View page iframe must have sandbox="" and src pointing to /messages/{id}/html
	resView, err := http.Get(fmt.Sprintf("%s/messages/%s", httpSrv.URL, id))
	if err != nil {
		t.Fatalf("GET /messages/%s failed: %v", id, err)
	}
	viewBody, _ := io.ReadAll(resView.Body)
	resView.Body.Close()
	if !strings.Contains(string(viewBody), `sandbox=""`) && !strings.Contains(string(viewBody), `sandbox="" src="/messages/`+id+`/html"`) {
		t.Errorf("iframe is not sandboxed: %s", string(viewBody))
	}

	// /messages/{id}/html must have CSP sandbox
	resHTML, err := http.Get(fmt.Sprintf("%s/messages/%s/html", httpSrv.URL, id))
	if err != nil {
		t.Fatalf("GET html failed: %v", err)
	}
	csp := resHTML.Header.Get("Content-Security-Policy")
	resHTML.Body.Close()
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("missing sandbox in CSP: %q", csp)
	}
	_ = srv
}

// 2. XSS in the list (html/template escaping)
func TestSecurity_XSSInList(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	xssSubject := "<script>alert(1)</script><b onmouseover=alert(2)>hello</b>"
	_ = smtp.SendMail(addr, nil, "x@a.com", []string{"y@b.com"}, []byte("Subject: "+xssSubject+"\r\n\r\nbody"))

	httpSrv := httptest.NewServer(srv.HTTPHandler())
	defer httpSrv.Close()

	res, err := http.Get(httpSrv.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	bodyStr := string(body)
	if strings.Contains(bodyStr, "<script>") || strings.Contains(bodyStr, "<b onmouseover") {
		t.Errorf("subject was not escaped in index: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "&lt;script&gt;") {
		t.Errorf("expected escaped &lt;script&gt; in body")
	}
}

// 3. Header injection through attachment filenames
func TestSecurity_HeaderInjectionInAttachmentFilename(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	injectionName := "payload.txt\r\nInjected-Header: evil\r\n\"x"
	msgAtt := strings.Join([]string{
		"From: evil@test.com",
		"To: victim@test.com",
		"Subject: Injection",
		"Content-Type: multipart/mixed; boundary=sep",
		"",
		"--sep",
		"Content-Type: application/octet-stream",
		"Content-Disposition: attachment; filename=\"" + injectionName + "\"",
		"",
		"data",
		"--sep--",
		"",
	}, "\r\n")
	_ = smtp.SendMail(addr, nil, "evil@test.com", []string{"victim@test.com"}, []byte(msgAtt))

	httpSrv := httptest.NewServer(srv.HTTPHandler())
	defer httpSrv.Close()

	msgs := srv.Messages()
	if len(msgs) == 0 {
		t.Fatalf("no message received")
	}
	res, err := http.Get(fmt.Sprintf("%s/api/messages/%s/attachments/0", httpSrv.URL, msgs[0].ID))
	if err != nil {
		t.Fatalf("GET attachment failed: %v", err)
	}
	defer res.Body.Close()
	if res.Header.Get("Injected-Header") != "" {
		t.Fatalf("header injection succeeded!")
	}
	cd := res.Header.Get("Content-Disposition")
	if strings.Contains(cd, "\r") || strings.Contains(cd, "\n") || strings.Contains(cd, "\"") {
		t.Errorf("Content-Disposition has unescaped controls or quotes: %q", cd)
	}
}

// 4. Memory exhaustion (limits test on parts & nesting)
func TestSecurity_MIMENestingLimit(t *testing.T) {
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{})
	defer cleanup()

	// Deeply nested multipart bomb
	var b strings.Builder
	b.WriteString("From: bomb@evil.test\r\nTo: victim@test.com\r\nSubject: Bomb\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=b0\r\n\r\n")
	for i := 0; i < 20; i++ {
		b.WriteString(fmt.Sprintf("--b%d\r\nContent-Type: multipart/mixed; boundary=b%d\r\n\r\n", i, i+1))
	}
	b.WriteString("deep body\r\n")
	for i := 20; i >= 0; i-- {
		b.WriteString(fmt.Sprintf("--b%d--\r\n", i))
	}

	_ = smtp.SendMail(addr, nil, "bomb@evil.test", []string{"victim@test.com"}, []byte(b.String()))
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message stored safely without stack overflow, got %d", len(msgs))
	}
}

// 5. Additional coverage tests for SMTP commands and HTTP API
func TestAdditionalCommandsAndAPI(t *testing.T) {
	events := make([]smtpmailcatcher.Event, 0)
	srv, addr, cleanup := startTestServer(t, smtpmailcatcher.Config{
		OnEvent: func(e smtpmailcatcher.Event) {
			events = append(events, e)
		},
	})
	defer cleanup()

	c, err := textproto.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer c.Close()
	c.ReadResponse(220)

	// VRFY
	c.Cmd("VRFY someone")
	c.ReadResponse(252)

	// Unrecognized command
	c.Cmd("UNKNOWN_COMMAND")
	c.ReadResponse(502)

	// HELO and then RSET
	c.Cmd("HELO local.test")
	c.ReadResponse(250)
	c.Cmd("RSET")
	c.ReadResponse(250)

	// Nested MAIL command error
	c.Cmd("MAIL FROM:<valid@test.com>")
	c.ReadResponse(250)
	c.Cmd("MAIL FROM:<again@test.com>")
	c.ReadResponse(503)

	// Bad MAIL syntax
	c.Cmd("RSET")
	c.ReadResponse(250)
	c.Cmd("MAIL NOTFROM")
	c.ReadResponse(501)

	// Bad RCPT syntax
	c.Cmd("MAIL FROM:<valid@test.com>")
	c.ReadResponse(250)
	c.Cmd("RCPT NOTTO")
	c.ReadResponse(501)

	// Bad DATA before RCPT
	c.Cmd("RSET")
	c.ReadResponse(250)
	c.Cmd("DATA")
	c.ReadResponse(503)

	// QUIT
	c.Cmd("QUIT")
	c.ReadResponse(221)

	// HTTP API Not Found tests
	httpSrv := httptest.NewServer(srv.HTTPHandler())
	defer httpSrv.Close()

	res404, err := http.Get(httpSrv.URL + "/non-existent")
	if err != nil {
		t.Fatalf("GET /non-existent failed: %v", err)
	}
	if res404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res404.StatusCode)
	}
	res404.Body.Close()

	resMsg404, err := http.Get(httpSrv.URL + "/api/messages/no-such-id")
	if err != nil {
		t.Fatalf("GET /api/messages/no-such-id failed: %v", err)
	}
	if resMsg404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing id, got %d", resMsg404.StatusCode)
	}
	resMsg404.Body.Close()

	resView404, err := http.Get(httpSrv.URL + "/messages/no-such-id")
	if err != nil {
		t.Fatalf("GET /messages/no-such-id failed: %v", err)
	}
	if resView404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing HTML view, got %d", resView404.StatusCode)
	}
	resView404.Body.Close()

	resHTML404, err := http.Get(httpSrv.URL + "/messages/no-such-id/html")
	if err != nil {
		t.Fatalf("GET /messages/no-such-id/html failed: %v", err)
	}
	if resHTML404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing HTML, got %d", resHTML404.StatusCode)
	}
	resHTML404.Body.Close()

	resAtt404, err := http.Get(httpSrv.URL + "/api/messages/no-such-id/attachments/0")
	if err != nil {
		t.Fatalf("GET /api/messages/no-such-id/attachments/0 failed: %v", err)
	}
	if resAtt404.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing attachment msg, got %d", resAtt404.StatusCode)
	}
	resAtt404.Body.Close()

	resAttBadIdx, err := http.Get(httpSrv.URL + "/api/messages/no-such-id/attachments/not-an-int")
	if err != nil {
		t.Fatalf("GET /api/messages/no-such-id/attachments/not-an-int failed: %v", err)
	}
	if resAttBadIdx.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-int attachment idx, got %d", resAttBadIdx.StatusCode)
	}
	resAttBadIdx.Body.Close()

	// Error representation
	libErr := &smtpmailcatcher.Error{Code: "test_err", Message: "something failed"}
	if !strings.Contains(libErr.Error(), "test_err: something failed") {
		t.Errorf("unexpected Error.Error(): %q", libErr.Error())
	}
	libErrNoMsg := &smtpmailcatcher.Error{Code: "just_code"}
	if libErrNoMsg.Error() != "just_code" {
		t.Errorf("unexpected Error.Error(): %q", libErrNoMsg.Error())
	}
}
