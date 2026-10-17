package crypto

import (
	"encoding/json"
	"os"
	"testing"
)

type vec struct {
	Key, BoxID, Nonce, Plaintext, Ciphertext, ReadToken, ReadTokenHash string
	V                                                                  int
}

func loadVec(t *testing.T) (vec, []byte, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../contract/crypto-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	s := func(k string) string { return m[k].(string) }
	v := vec{Key: s("key"), BoxID: s("boxId"), Nonce: s("nonce"), Plaintext: s("plaintext"),
		Ciphertext: s("ciphertext"), ReadToken: s("readToken"), ReadTokenHash: s("readTokenHash"), V: int(m["v"].(float64))}
	key, err := UnB64(v.Key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := UnB64(v.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	return v, key, nonce
}

func TestCryptoVector(t *testing.T) {
	v, key, nonce := loadVec(t)
	if got := ReadToken(key); got != v.ReadToken {
		t.Fatalf("ReadToken = %s", got)
	}
	if got := TokenHash(v.ReadToken); got != v.ReadTokenHash {
		t.Fatalf("TokenHash = %s", got)
	}
	env := SealWithNonce(key, nonce, v.BoxID, v.V, []byte(v.Plaintext))
	if env.Ciphertext != v.Ciphertext || env.Nonce != v.Nonce || env.V != 1 {
		t.Fatalf("envelope mismatch: %+v", env)
	}
	pt, err := Open(key, v.BoxID, env)
	if err != nil || string(pt) != v.Plaintext {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Open(key, "b_bbbbbbbbbbbbbbbbbbbbbbbbbb", env); err == nil {
		t.Fatal("different box id must fail")
	}
}

func TestOpenRejects(t *testing.T) {
	v, key, nonce := loadVec(t)
	env := SealWithNonce(key, nonce, v.BoxID, v.V, []byte(v.Plaintext))
	bad := env
	bad.V = 2
	if _, err := Open(key, v.BoxID, bad); err == nil {
		t.Error("wrong version accepted")
	}
	bad = env
	bad.Nonce = B64([]byte("short"))
	if _, err := Open(key, v.BoxID, bad); err == nil {
		t.Error("short nonce accepted")
	}
	bad = env
	bad.Ciphertext = "!!!"
	if _, err := Open(key, v.BoxID, bad); err == nil {
		t.Error("bad base64 accepted")
	}
	ct, _ := UnB64(env.Ciphertext)
	ct[len(ct)-1] ^= 1
	bad = env
	bad.Ciphertext = B64(ct)
	if _, err := Open(key, v.BoxID, bad); err == nil {
		t.Error("tampered tag accepted")
	}
	if _, err := Open(key[:16], v.BoxID, env); err == nil {
		t.Error("bad key length accepted")
	}
}

func TestSealRoundTripAndNewKey(t *testing.T) {
	key, err := NewKey()
	if err != nil || len(key) != 32 {
		t.Fatal("NewKey")
	}
	a, err := Seal(key, "b_x", 1, []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Seal(key, "b_x", 1, []byte("hi"))
	if a.Nonce == b.Nonce {
		t.Error("nonce reused")
	}
	pt, err := Open(key, "b_x", a)
	if err != nil || string(pt) != "hi" {
		t.Fatal("round trip")
	}
}
