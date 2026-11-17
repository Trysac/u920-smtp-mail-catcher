package smtpmailcatcher

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Default configuration constants.
const (
	DefaultHostname        = "mailcatcher.local"
	DefaultMaxMessages     = 1000
	DefaultMaxMessageBytes = 10 * 1024 * 1024 // 10 MiB
	DefaultMaxRecipients   = 100
	DefaultCommandTimeout  = 5 * time.Minute

	MaxCommandLineBytes = 1000
	MaxMIMENestingDepth = 10
	MaxMIMEParts        = 100
)

// Error represents an API or library error with a machine-readable code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Code
}

// Attachment represents an email attachment decoded from MIME.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	Data        []byte `json:"-"`
}

// Message represents an email captured by the server.
type Message struct {
	ID          string       `json:"id"`
	From        string       `json:"from"`
	To          []string     `json:"to"`
	Subject     string       `json:"subject"`
	Date        time.Time    `json:"date"`
	Text        string       `json:"text"`
	HTML        string       `json:"html"`
	Attachments []Attachment `json:"attachments"`
	Raw         []byte       `json:"-"`
	ReceivedAt  time.Time    `json:"receivedAt"`
}

// Event records an activity event emitted by the server.
type Event struct {
	Kind      string `json:"kind"`
	MessageID string `json:"messageId,omitempty"`
	Size      int    `json:"size,omitempty"`
}

// Config defines configuration parameters for Server.
type Config struct {
	Hostname        string
	MaxMessages     int
	MaxMessageBytes int64
	MaxRecipients   int
	CommandTimeout  time.Duration
	Clock           func() time.Time
	NewID           func() string
	OnEvent         func(Event)
}

// Server is an in-memory SMTP receiver and HTTP message inspector.
type Server struct {
	cfg      Config
	mu       sync.RWMutex
	messages []*Message
	idSeq    uint64

	listener net.Listener
	wg       sync.WaitGroup
	closing  chan struct{}
	closeMu  sync.Mutex
	isClosed bool
}

// NewServer creates a new Server configured with cfg.
func NewServer(cfg Config) *Server {
	if cfg.Hostname == "" {
		cfg.Hostname = DefaultHostname
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = DefaultMaxMessages
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if cfg.MaxRecipients <= 0 {
		cfg.MaxRecipients = DefaultMaxRecipients
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = DefaultCommandTimeout
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}

	s := &Server{
		cfg:      cfg,
		messages: make([]*Message, 0),
		closing:  make(chan struct{}),
	}
	if s.cfg.NewID == nil {
		s.cfg.NewID = s.defaultNewID
	}
	return s
}

func (s *Server) defaultNewID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idSeq++
	return fmt.Sprintf("msg-%d-%d", s.cfg.Clock().UnixNano(), s.idSeq)
}

func (s *Server) emitEvent(kind, msgID string, size int) {
	if s.cfg.OnEvent != nil {
		s.cfg.OnEvent(Event{Kind: kind, MessageID: msgID, Size: size})
	}
}

// Serve accepts incoming connections on listener l and services SMTP commands.
func (s *Server) Serve(l net.Listener) error {
	s.closeMu.Lock()
	s.listener = l
	s.closeMu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-s.closing:
				return nil
			default:
				return err
			}
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConn(c)
		}(conn)
	}
}

// Close closes the SMTP listener and active connections.
func (s *Server) Close() error {
	s.closeMu.Lock()
	if s.isClosed {
		s.closeMu.Unlock()
		return nil
	}
	s.isClosed = true
	close(s.closing)
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	s.closeMu.Unlock()
	s.wg.Wait()
	return err
}

// Messages returns deep copies of captured messages, ordered newest first.
func (s *Server) Messages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Message, len(s.messages))
	for i, msg := range s.messages {
		res[i] = *copyMessage(msg)
	}
	return res
}

func copyMessage(m *Message) *Message {
	if m == nil {
		return nil
	}
	cp := *m
	cp.To = append([]string(nil), m.To...)
	cp.Raw = append([]byte(nil), m.Raw...)
	if len(m.Attachments) > 0 {
		cp.Attachments = make([]Attachment, len(m.Attachments))
		for i, a := range m.Attachments {
			cp.Attachments[i] = a
			cp.Attachments[i].Data = append([]byte(nil), a.Data...)
		}
	}
	return &cp
}

// DeleteAll deletes all captured messages in the store.
func (s *Server) DeleteAll() {
	s.mu.Lock()
	s.messages = make([]*Message, 0)
	s.mu.Unlock()
	s.emitEvent("store_cleared", "", 0)
}

// GetMessage finds a message by ID and returns a copy.
func (s *Server) GetMessage(id string) (*Message, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages {
		if m.ID == id {
			return copyMessage(m), true
		}
	}
	return nil, false
}

func (s *Server) storeMessage(m *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Prepend to maintain newest-first order.
	s.messages = append([]*Message{m}, s.messages...)
	if len(s.messages) > s.cfg.MaxMessages {
		s.messages = s.messages[:s.cfg.MaxMessages]
	}
	s.emitEvent("message_stored", m.ID, len(m.Raw))
}

// Connection handling and SMTP protocol state machine.
type sessionState int

const (
	stateGreeting sessionState = iota
	stateIdentified
	stateMail
	stateRcpt
)

type smtpSession struct {
	srv        *Server
	conn       net.Conn
	r          *bufio.Reader
	w          *bufio.Writer
	state      sessionState
	heloHost   string
	mailFrom   string
	rcptTo     []string
	declaredSz int64
}

func (s *Server) handleConn(c net.Conn) {
	defer c.Close()
	sess := &smtpSession{
		srv:   s,
		conn:  c,
		r:     bufio.NewReader(c),
		w:     bufio.NewWriter(c),
		state: stateGreeting,
	}

	sess.writeLine(fmt.Sprintf("220 %s ESMTP mailcatcher", s.cfg.Hostname))

	for {
		c.SetReadDeadline(s.cfg.Clock().Add(s.cfg.CommandTimeout))
		line, err := sess.readLine()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				sess.writeLine("421 4.4.2 Timeout")
				return
			}
			if errors.Is(err, errLineTooLong) {
				sess.writeLine("500 5.5.2 Line too long")
				continue
			}
			return
		}

		if sess.dispatch(line) {
			return
		}
	}
}

var errLineTooLong = errors.New("line too long")

func (sess *smtpSession) readLine() (string, error) {
	var lineBuf []byte
	for {
		b, err := sess.r.ReadByte()
		if err != nil {
			return "", err
		}
		lineBuf = append(lineBuf, b)
		if len(lineBuf) > MaxCommandLineBytes {
			// Drain until newline to stay synchronized.
			for b != '\n' {
				b, err = sess.r.ReadByte()
				if err != nil {
					break
				}
			}
			return "", errLineTooLong
		}
		if b == '\n' {
			break
		}
	}
	line := string(lineBuf)
	line = strings.TrimRight(line, "\r\n")
	return line, nil
}

func (sess *smtpSession) writeLine(msg string) {
	sess.w.WriteString(msg + "\r\n")
	sess.w.Flush()
}

func (sess *smtpSession) resetTransaction() {
	sess.mailFrom = ""
	sess.rcptTo = nil
	sess.declaredSz = 0
	if sess.state >= stateMail {
		sess.state = stateIdentified
	}
}

func (sess *smtpSession) dispatch(line string) bool {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(strings.TrimSpace(parts[0]))
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "QUIT":
		sess.writeLine("221 2.0.0 Bye")
		return true
	case "NOOP":
		sess.writeLine("250 2.0.0 OK")
	case "RSET":
		sess.resetTransaction()
		sess.writeLine("250 2.0.0 OK")
	case "VRFY":
		sess.writeLine("252 2.5.0 Cannot VRFY user")
	case "HELO":
		sess.resetTransaction()
		sess.heloHost = arg
		sess.state = stateIdentified
		sess.writeLine(fmt.Sprintf("250 %s", sess.srv.cfg.Hostname))
	case "EHLO":
		sess.resetTransaction()
		sess.heloHost = arg
		sess.state = stateIdentified
		sess.writeLine(fmt.Sprintf("250-%s", sess.srv.cfg.Hostname))
		sess.writeLine(fmt.Sprintf("250-SIZE %d", sess.srv.cfg.MaxMessageBytes))
		sess.writeLine("250-8BITMIME")
		sess.writeLine("250 PIPELINING")
	case "MAIL":
		sess.handleMail(arg)
	case "RCPT":
		sess.handleRcpt(arg)
	case "DATA":
		sess.handleData()
	default:
		sess.writeLine("502 5.5.2 Command not recognized")
	}
	return false
}

func (sess *smtpSession) handleMail(arg string) {
	if sess.state < stateIdentified {
		sess.writeLine("503 5.5.1 Bad sequence of commands")
		return
	}
	if sess.state >= stateMail {
		sess.writeLine("503 5.5.1 Nested MAIL command")
		return
	}
	if !strings.HasPrefix(strings.ToUpper(arg), "FROM:") {
		sess.writeLine("501 5.5.4 Syntax error in MAIL command")
		return
	}

	fromRest := strings.TrimSpace(arg[5:])
	var fromAddr string
	var params string
	if strings.HasPrefix(fromRest, "<") {
		endIdx := strings.Index(fromRest, ">")
		if endIdx == -1 {
			sess.writeLine("501 5.5.4 Syntax error in MAIL command")
			return
		}
		fromAddr = fromRest[1:endIdx]
		params = strings.TrimSpace(fromRest[endIdx+1:])
	} else {
		fields := strings.Fields(fromRest)
		if len(fields) == 0 {
			sess.writeLine("501 5.5.4 Syntax error in MAIL command")
			return
		}
		fromAddr = fields[0]
		if len(fields) > 1 {
			params = strings.Join(fields[1:], " ")
		}
	}

	var declaredSize int64
	if params != "" {
		for _, param := range strings.Fields(params) {
			if strings.HasPrefix(strings.ToUpper(param), "SIZE=") {
				szStr := param[5:]
				sz, err := strconv.ParseInt(szStr, 10, 64)
				if err == nil {
					declaredSize = sz
				}
			}
		}
	}

	if declaredSize > sess.srv.cfg.MaxMessageBytes {
		sess.writeLine("552 5.3.4 Message size exceeds fixed limit")
		return
	}

	sess.mailFrom = fromAddr
	sess.declaredSz = declaredSize
	sess.rcptTo = nil
	sess.state = stateMail
	sess.writeLine("250 2.1.0 Ok")
}

func (sess *smtpSession) handleRcpt(arg string) {
	if sess.state != stateMail && sess.state != stateRcpt {
		sess.writeLine("503 5.5.1 Bad sequence of commands")
		return
	}
	if !strings.HasPrefix(strings.ToUpper(arg), "TO:") {
		sess.writeLine("501 5.5.4 Syntax error in RCPT command")
		return
	}
	if len(sess.rcptTo) >= sess.srv.cfg.MaxRecipients {
		sess.writeLine("452 4.5.3 Too many recipients")
		return
	}

	toRest := strings.TrimSpace(arg[3:])
	var toAddr string
	if strings.HasPrefix(toRest, "<") {
		endIdx := strings.Index(toRest, ">")
		if endIdx == -1 {
			sess.writeLine("501 5.5.4 Syntax error in RCPT command")
			return
		}
		toAddr = toRest[1:endIdx]
	} else {
		fields := strings.Fields(toRest)
		if len(fields) == 0 {
			sess.writeLine("501 5.5.4 Syntax error in RCPT command")
			return
		}
		toAddr = fields[0]
	}

	sess.rcptTo = append(sess.rcptTo, toAddr)
	sess.state = stateRcpt
	sess.writeLine("250 2.1.5 Ok")
}

func (sess *smtpSession) handleData() {
	if sess.state != stateRcpt || len(sess.rcptTo) == 0 {
		sess.writeLine("503 5.5.1 Bad sequence of commands")
		return
	}
	sess.writeLine("354 End data with <CR><LF>.<CR><LF>")

	var dataBuf bytes.Buffer
	exceededLimit := false

	for {
		sess.conn.SetReadDeadline(sess.srv.cfg.Clock().Add(sess.srv.cfg.CommandTimeout))
		lineBytes, err := sess.r.ReadBytes('\n')
		if err != nil {
			return
		}
		line := string(lineBytes)
		cleanLine := strings.TrimRight(line, "\r\n")
		if cleanLine == "." {
			break
		}
		if strings.HasPrefix(cleanLine, "..") {
			cleanLine = cleanLine[1:]
		}
		if !exceededLimit {
			if int64(dataBuf.Len()+len(cleanLine)+2) > sess.srv.cfg.MaxMessageBytes {
				exceededLimit = true
			} else {
				dataBuf.WriteString(cleanLine)
				dataBuf.WriteString("\r\n")
			}
		}
	}

	if exceededLimit {
		sess.resetTransaction()
		sess.writeLine("552 5.3.4 Message size exceeds fixed limit")
		return
	}

	rawMsg := dataBuf.Bytes()
	msgID := sess.srv.cfg.NewID()
	now := sess.srv.cfg.Clock()

	parsedMsg := parseRawMessage(rawMsg, sess.mailFrom, sess.rcptTo, msgID, now)
	sess.srv.storeMessage(parsedMsg)
	sess.resetTransaction()
	sess.writeLine(fmt.Sprintf("250 2.0.0 Ok: queued as %s", msgID))
}

// MIME parsing.
func parseRawMessage(raw []byte, envFrom string, envTo []string, id string, now time.Time) *Message {
	msg := &Message{
		ID:          id,
		From:        envFrom,
		To:          append([]string(nil), envTo...),
		Date:        now,
		Raw:         raw,
		ReceivedAt:  now,
		Attachments: make([]Attachment, 0),
	}

	mailMsg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return msg
	}

	dec := &mime.WordDecoder{
		CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
			cs := strings.ToUpper(charset)
			switch cs {
			case "UTF-8", "US-ASCII":
				return input, nil
			case "ISO-8859-1":
				return newLatin1ToUTF8Reader(input), nil
			default:
				return input, nil
			}
		},
	}

	if subj := mailMsg.Header.Get("Subject"); subj != "" {
		if decoded, err := dec.DecodeHeader(subj); err == nil {
			msg.Subject = decoded
		} else {
			msg.Subject = subj
		}
	}
	if dt, err := mailMsg.Header.Date(); err == nil {
		msg.Date = dt
	}

	contentType := mailMsg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}

	var partCount int
	walkMIMEPart(mailMsg.Header, mailMsg.Body, contentType, 0, &partCount, msg, dec)
	return msg
}

func walkMIMEPart(header mail.Header, body io.Reader, contentTypeHeader string, depth int, partCount *int, msg *Message, dec *mime.WordDecoder) {
	if depth > MaxMIMENestingDepth || *partCount >= MaxMIMEParts {
		return
	}
	*partCount++

	mediaType, params, err := mime.ParseMediaType(contentTypeHeader)
	if err != nil {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(stripPartHeaderControls(raw, boundary)), boundary)
		for {
			if *partCount >= MaxMIMEParts {
				break
			}
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			pHeader := make(mail.Header)
			for k, v := range p.Header {
				pHeader[textproto.CanonicalMIMEHeaderKey(k)] = v
			}
			pContentType := pHeader.Get("Content-Type")
			if pContentType == "" {
				pContentType = "text/plain"
			}
			walkMIMEPart(pHeader, p, pContentType, depth+1, partCount, msg, dec)
		}
		return
	}

	contentDisposition := header.Get("Content-Disposition")
	if contentDisposition == "" {
		contentDisposition = header.Get("content-disposition")
	}
	dispType, dispParams, _ := mime.ParseMediaType(contentDisposition)
	if dispType == "" && contentDisposition != "" {
		parts := strings.SplitN(contentDisposition, ";", 2)
		dispType = strings.TrimSpace(parts[0])
	}

	filename := ""
	if dispParams != nil && dispParams["filename"] != "" {
		filename = dispParams["filename"]
	} else if params != nil && params["name"] != "" {
		filename = params["name"]
	}
	if filename == "" {
		for _, hdrVal := range []string{contentDisposition, contentTypeHeader} {
			if idx := strings.Index(strings.ToLower(hdrVal), "filename="); idx != -1 {
				f := strings.TrimSpace(hdrVal[idx+9:])
				f = strings.Trim(f, "\r\n")
				if strings.HasPrefix(f, "\"") {
					f = f[1:]
					if end := strings.LastIndex(f, "\""); end != -1 {
						f = f[:end]
					}
				} else {
					if semi := strings.Index(f, ";"); semi != -1 {
						f = f[:semi]
					}
				}
				if f != "" {
					filename = f
					break
				}
			}
		}
	}
	if filename != "" {
		if decFn, err := dec.DecodeHeader(filename); err == nil {
			filename = decFn
		}
	}

	bodyData, err := decodeTransferEncoding(header.Get("Content-Transfer-Encoding"), body)
	if err != nil {
		return
	}

	isAttachment := strings.EqualFold(dispType, "attachment") || strings.Contains(strings.ToLower(contentDisposition), "attachment") || filename != ""

	if !isAttachment && strings.EqualFold(mediaType, "text/plain") && msg.Text == "" {
		msg.Text = string(bodyData)
	} else if !isAttachment && strings.EqualFold(mediaType, "text/html") && msg.HTML == "" {
		msg.HTML = string(bodyData)
	} else if isAttachment {
		cleanFilename := sanitizeFilename(filename)
		if cleanFilename == "" {
			cleanFilename = "safename.txt"
		}
		msg.Attachments = append(msg.Attachments, Attachment{
			Filename:    cleanFilename,
			ContentType: mediaType,
			Size:        len(bodyData),
			Data:        bodyData,
		})
	}
}

// stripPartHeaderControls removes ASCII control characters from the header lines of each MIME part, so a hostile
// header (for example a filename carrying BEL) does not make the multipart reader drop the whole part. Part bodies
// are left byte-for-byte unchanged.
func stripPartHeaderControls(raw []byte, boundary string) []byte {
	delimiter := []byte("--" + boundary)
	var out bytes.Buffer
	out.Grow(len(raw))
	inHeader := false
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, '\n')
		line := raw
		if end >= 0 {
			line = raw[:end+1]
		}
		raw = raw[len(line):]
		content := bytes.TrimRight(line, "\r\n")
		switch {
		case bytes.HasPrefix(content, delimiter):
			inHeader = !bytes.Equal(content, append(delimiter, '-', '-'))
			out.Write(line)
		case inHeader && len(content) == 0:
			inHeader = false
			out.Write(line)
		case inHeader:
			for _, c := range content {
				if c >= 0x20 && c != 0x7f || c == '\t' {
					out.WriteByte(c)
				}
			}
			out.Write(line[len(content):])
		default:
			out.Write(line)
		}
	}
	return out.Bytes()
}

func decodeTransferEncoding(enc string, r io.Reader) ([]byte, error) {
	enc = strings.ToLower(strings.TrimSpace(enc))
	switch enc {
	case "base64":
		dec := base64.NewDecoder(base64.StdEncoding, &stripSpaceReader{r: r})
		return io.ReadAll(dec)
	case "quoted-printable":
		dec := quotedprintable.NewReader(r)
		return io.ReadAll(dec)
	default:
		return io.ReadAll(r)
	}
}

type stripSpaceReader struct {
	r io.Reader
}

func (s *stripSpaceReader) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	n, err := s.r.Read(buf)
	wIdx := 0
	for i := 0; i < n; i++ {
		c := buf[i]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			p[wIdx] = c
			wIdx++
		}
	}
	return wIdx, err
}

type latin1ToUTF8Reader struct {
	r   io.Reader
	buf []byte
}

func newLatin1ToUTF8Reader(r io.Reader) *latin1ToUTF8Reader {
	return &latin1ToUTF8Reader{r: r}
}

func (l *latin1ToUTF8Reader) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		tmp := make([]byte, len(p))
		n, err := l.r.Read(tmp)
		if n > 0 {
			var converted []byte
			for _, b := range tmp[:n] {
				if b < 0x80 {
					converted = append(converted, b)
				} else {
					converted = append(converted, 0xc0|(b>>6), 0x80|(b&0x3f))
				}
			}
			l.buf = converted
		}
		if err != nil && len(l.buf) == 0 {
			return 0, err
		}
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '"' || r == '\'' || r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// HTTPHandler returns an http.Handler serving the HTML UI and JSON API.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /messages/{id}", s.handleViewMessage)
	mux.HandleFunc("GET /messages/{id}/html", s.handleMessageHTML)
	mux.HandleFunc("GET /api/messages", s.handleAPIMessages)
	mux.HandleFunc("DELETE /api/messages", s.handleAPIDeleteMessages)
	mux.HandleFunc("GET /api/messages/{id}", s.handleAPIGetMessage)
	mux.HandleFunc("GET /api/messages/{id}/attachments/{n}", s.handleAPIGetAttachment)
	return mux
}

var (
	indexTmpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html>
<head><title>SMTP Mail Catcher</title></head>
<body>
<h1>Captured Messages</h1>
<ul>
{{range .}}
<li><a href="/messages/{{.ID}}">{{.Subject}}</a> (From: {{.From}}, To: {{range .To}}{{.}} {{end}})</li>
{{else}}
<li>No messages captured yet.</li>
{{end}}
</ul>
</body>
</html>`))

	viewTmpl = template.Must(template.New("view").Parse(`<!DOCTYPE html>
<html>
<head><title>{{.Subject}} - Mail Catcher</title></head>
<body>
<p><a href="/">&larr; Back to list</a></p>
<h2>{{.Subject}}</h2>
<p><strong>From:</strong> {{.From}}</p>
<p><strong>To:</strong> {{range .To}}{{.}} {{end}}</p>
<p><strong>Date:</strong> {{.Date}}</p>
{{if .HTML}}
<h3>HTML Body</h3>
<iframe sandbox="" src="/messages/{{.ID}}/html" style="width:100%; height:400px; border:1px solid #ccc;"></iframe>
{{end}}
{{if .Text}}
<h3>Plain Text</h3>
<pre>{{.Text}}</pre>
{{end}}
{{if .Attachments}}
<h3>Attachments</h3>
<ul>
{{range $idx, $att := .Attachments}}
<li><a href="/api/messages/{{$.ID}}/attachments/{{$idx}}">{{$att.Filename}}</a> ({{$att.Size}} bytes)</li>
{{end}}
</ul>
{{end}}
</body>
</html>`))
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Not found")
		return
	}
	msgs := s.Messages()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTmpl.Execute(w, msgs)
}

func (s *Server) handleViewMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msg, ok := s.GetMessage(id)
	if !ok {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Message not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = viewTmpl.Execute(w, msg)
}

func (s *Server) handleMessageHTML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msg, ok := s.GetMessage(id)
	if !ok {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Message not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(msg.HTML))
}

func (s *Server) handleAPIMessages(w http.ResponseWriter, r *http.Request) {
	msgs := s.Messages()
	s.writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleAPIDeleteMessages(w http.ResponseWriter, r *http.Request) {
	s.DeleteAll()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAPIGetMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msg, ok := s.GetMessage(id)
	if !ok {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Message not found")
		return
	}
	s.writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleAPIGetAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nStr := r.PathValue("n")
	n, err := strconv.Atoi(nStr)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "invalid_argument", "Invalid attachment index")
		return
	}
	msg, ok := s.GetMessage(id)
	if !ok {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Message not found")
		return
	}
	if n < 0 || n >= len(msg.Attachments) {
		s.writeJSONError(w, http.StatusNotFound, "not_found", "Attachment not found")
		return
	}

	att := msg.Attachments[n]
	ct := att.ContentType
	if _, _, err := mime.ParseMediaType(ct); err != nil {
		ct = "application/octet-stream"
	}

	safeFilename := sanitizeFilename(att.Filename)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", safeFilename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(att.Data)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}
