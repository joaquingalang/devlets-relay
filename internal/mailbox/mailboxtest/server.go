// Package mailboxtest is an in-memory fake of the relay Worker for tests. It
// mirrors the real status codes and records every request.
package mailboxtest

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/crypto"
)

// RecordedRequest is one request the server saw.
type RecordedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type box struct {
	readHash, writeHash string
	reading             *crypto.Envelope
	receivedAt          string
	hello               *crypto.Envelope
}

// Server is the fake mailbox.
type Server struct {
	URL string

	srv        *httptest.Server
	mu         sync.Mutex
	boxes      map[string]*box
	reqs       []RecordedRequest
	retryAfter int // >0: answer every request 429 with this Retry-After
	status     int // !=0: answer every request with this status
}

// New starts a fake mailbox. Call Close when done.
func New() *Server {
	s := &Server{boxes: map[string]*box{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL
	return s
}

func (s *Server) Close() { s.srv.Close() }

// Requests returns a copy of every request seen, phone-side helpers included.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RecordedRequest(nil), s.reqs...)
}

// DeleteBox removes a box server-side (as retention or the phone would).
func (s *Server) DeleteBox(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.boxes, id)
}

// ForceRateLimit makes every later request answer 429 with Retry-After secs; 0 clears it.
func (s *Server) ForceRateLimit(secs int) { s.mu.Lock(); s.retryAfter = secs; s.mu.Unlock() }

// ForceStatus makes every later request answer with code; 0 clears it.
func (s *Server) ForceStatus(code int) { s.mu.Lock(); s.status = code; s.mu.Unlock() }

// PhoneGet plays the phone: GET the box with the read token. It returns the
// envelope (nil unless the status is 200) and the status.
func (s *Server) PhoneGet(boxID, readToken string) (*crypto.Envelope, int) {
	st, data := s.call("GET", "/v1/relay/boxes/"+boxID, readToken, nil)
	if st != 200 {
		return nil, st
	}
	var env crypto.Envelope
	if json.Unmarshal(data, &env) != nil {
		return nil, 500
	}
	return &env, 200
}

// PhoneHello plays the phone: POST the hello envelope with the read token.
func (s *Server) PhoneHello(boxID, readToken string, env crypto.Envelope) int {
	b, _ := json.Marshal(env)
	st, _ := s.call("POST", "/v1/relay/boxes/"+boxID+"/hello", readToken, b)
	return st
}

func (s *Server) call(method, path, token string, body []byte) (int, []byte) {
	req, _ := http.NewRequest(method, s.URL+path, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "mailboxtest-phone")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return crypto.B64(sum[:])
}

func match(token, h string) bool {
	return subtle.ConstantTimeCompare([]byte(hash(token)), []byte(h)) == 1
}

func reply(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.WriteHeader(code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) { reply(w, 404, map[string]string{"error": "not_found"}) }

func badRequest(w http.ResponseWriter) { reply(w, 400, map[string]string{"error": "bad_request"}) }

func strict(body []byte, v any) bool {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8*1024+1))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, RecordedRequest{r.Method, r.URL.Path, r.Header.Clone(), body})

	if s.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(s.retryAfter))
		reply(w, 429, map[string]string{"error": "rate_limited"})
		return
	}
	if s.status != 0 {
		reply(w, s.status, map[string]string{"error": "internal"})
		return
	}
	if len(body) > 8*1024 {
		reply(w, 413, map[string]string{"error": "too_large"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // v1 relay boxes [id [hello]]
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "relay" || parts[2] != "boxes" || len(parts) > 5 {
		notFound(w)
		return
	}
	if len(parts) == 3 {
		var in struct {
			ReadTokenHash string `json:"readTokenHash"`
		}
		if r.Method != "POST" {
			notFound(w)
		} else if !strict(body, &in) || in.ReadTokenHash == "" {
			badRequest(w)
		} else {
			rb := make([]byte, 16)
			_, _ = rand.Read(rb)
			id := "b_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(rb))[:26]
			wb := make([]byte, 32)
			_, _ = rand.Read(wb)
			wt := crypto.B64(wb)
			s.boxes[id] = &box{readHash: in.ReadTokenHash, writeHash: hash(wt)}
			reply(w, 201, map[string]string{"boxId": id, "writeToken": wt})
		}
		return
	}
	b := s.boxes[parts[3]]
	hello := len(parts) == 5
	if b == nil || (hello && parts[4] != "hello") {
		notFound(w)
		return
	}
	isRead := match(token, b.readHash)
	isWrite := match(token, b.writeHash)
	var env crypto.Envelope
	switch {
	case hello && r.Method == "POST" && isRead:
		if !strict(body, &env) {
			badRequest(w)
			return
		}
		b.hello = &env
		reply(w, 204, nil)
	case hello && r.Method == "GET" && isWrite:
		if b.hello == nil {
			reply(w, 204, nil)
			return
		}
		reply(w, 200, b.hello)
	case !hello && r.Method == "PUT" && isWrite:
		if !strict(body, &env) {
			badRequest(w)
			return
		}
		b.reading = &env
		b.receivedAt = strconv.FormatInt(time.Now().UnixMilli(), 10)
		reply(w, 204, nil)
	case !hello && r.Method == "GET" && isRead:
		if b.reading == nil {
			reply(w, 204, nil)
			return
		}
		etag := `"` + b.receivedAt + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", etag)
		reply(w, 200, map[string]any{"v": b.reading.V, "nonce": b.reading.Nonce,
			"ciphertext": b.reading.Ciphertext, "receivedAt": b.receivedAt})
	case !hello && r.Method == "DELETE" && (isRead || isWrite):
		delete(s.boxes, parts[3])
		reply(w, 204, nil)
	default:
		notFound(w)
	}
}
