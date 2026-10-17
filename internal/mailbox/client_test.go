package mailbox_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/joaquingalang/devlets-relay/internal/crypto"
	"github.com/joaquingalang/devlets-relay/internal/mailbox"
	"github.com/joaquingalang/devlets-relay/internal/mailbox/mailboxtest"
)

func newClient(s *mailboxtest.Server) *mailbox.Client {
	return &mailbox.Client{Base: s.URL, Version: "1.2.3"}
}

func TestRoundTrip(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	ctx := context.Background()
	key := make([]byte, 32)
	rt := crypto.ReadToken(key)

	id, wt, err := c.Create(ctx, crypto.TokenHash(rt))
	if err != nil || id == "" || wt == "" {
		t.Fatalf("create: %q %q %v", id, wt, err)
	}
	env, err := crypto.Seal(key, id, 1, []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(ctx, id, wt, env); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, status := s.PhoneGet(id, rt)
	if status != 200 || got == nil || got.Ciphertext != env.Ciphertext {
		t.Fatalf("phone get: %v %d", got, status)
	}
	h, err := c.Hello(ctx, id, wt)
	if err != nil || h != nil {
		t.Fatalf("hello before phone: %v %v", h, err)
	}
	henv, _ := crypto.Seal(key, id, 1, []byte(`{"phone":"Pixel"}`))
	if st := s.PhoneHello(id, rt, henv); st != 204 {
		t.Fatalf("phone hello status %d", st)
	}
	h, err = c.Hello(ctx, id, wt)
	if err != nil || h == nil || h.Ciphertext != henv.Ciphertext {
		t.Fatalf("hello: %v %v", h, err)
	}
	if err := c.Delete(ctx, id, wt); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.Put(ctx, id, wt, env); !errors.Is(err, mailbox.ErrGone) {
		t.Fatalf("put after delete: %v", err)
	}
}

func TestPutAfterDeleteBoxIsGone(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	id, wt, _ := c.Create(context.Background(), "hash")
	s.DeleteBox(id)
	err := c.Put(context.Background(), id, wt, crypto.Envelope{V: 1})
	if !errors.Is(err, mailbox.ErrGone) {
		t.Fatalf("want ErrGone, got %v", err)
	}
	if _, err := c.Hello(context.Background(), id, wt); !errors.Is(err, mailbox.ErrGone) {
		t.Fatalf("hello want ErrGone, got %v", err)
	}
}

func TestRateLimited(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	id, wt, _ := c.Create(context.Background(), "hash")
	s.ForceRateLimit(42)
	err := c.Put(context.Background(), id, wt, crypto.Envelope{V: 1})
	var rl *mailbox.RateLimited
	if !errors.As(err, &rl) || rl.RetryAfter != 42*time.Second {
		t.Fatalf("want RateLimited{42s}, got %v", err)
	}
}

func TestUserAgentAndNoTokenInErrors(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	id, wt, _ := c.Create(context.Background(), "hash")
	want := "devlets/1.2.3 (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	for _, r := range s.Requests() {
		if r.Header.Get("User-Agent") != want {
			t.Fatalf("UA %q want %q", r.Header.Get("User-Agent"), want)
		}
	}
	err := c.Put(context.Background(), id, "wrong-token-value", crypto.Envelope{V: 1})
	if err == nil || !errors.Is(err, mailbox.ErrGone) {
		t.Fatalf("wrong token: %v", err)
	}
	_ = wt
}

func TestUnexpectedStatusMessage(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	s.ForceStatus(500)
	_, _, err := c.Create(context.Background(), "hash")
	if err == nil || errors.Is(err, mailbox.ErrGone) {
		t.Fatalf("want generic error, got %v", err)
	}
}

func TestOnlyTheRelay404IsGone(t *testing.T) {
	s := mailboxtest.New()
	defer s.Close()
	c := newClient(s)
	id, wt, _ := c.Create(context.Background(), "hash")
	s.ForceStatus(404) // a 404 with another body, e.g. a proxy or a wrong base
	err := c.Put(context.Background(), id, wt, crypto.Envelope{V: 1})
	var se *mailbox.StatusError
	if errors.Is(err, mailbox.ErrGone) || !errors.As(err, &se) || se.Status != 404 {
		t.Fatalf("want StatusError 404, got %v", err)
	}
}
