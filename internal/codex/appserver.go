// Package codex connects Codex to the relay: it chains Codex's `notify`
// program, hooks and unhooks it in config.toml with line-level edits, and
// reads the rate limits from `codex app-server` over stdio.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/tidwall/gjson"

	"github.com/joaquingalang/devlets-relay/internal/contract"
	"github.com/joaquingalang/devlets-relay/internal/store"
)

// ClientVersion is sent to app-server in `initialize`. The CLI sets it.
var ClientVersion = "dev"

// readTimeout bounds one whole read (tests shorten it).
var readTimeout = 20 * time.Second

// mapRateLimits turns the account/rateLimits/read result into a Tool. Only
// primary, secondary and planType are read; rateLimitsByLimitId and the
// account id are ignored. Null windows are skipped. ReadAt is left unset.
func mapRateLimits(result []byte) (contract.Tool, error) {
	var tool contract.Tool
	limits := gjson.GetBytes(result, "rateLimits")
	if !limits.IsObject() {
		return tool, errors.New("Codex rate limits have an unexpected shape")
	}
	for _, name := range []string{"primary", "secondary"} {
		w := limits.Get(name)
		used, reset := w.Get("usedPercent"), w.Get("resetsAt")
		if !w.IsObject() || used.Type != gjson.Number || reset.Type != gjson.Number {
			continue
		}
		p := used.Float()
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		win := contract.Window{ID: name, UsedPercent: p, ResetsAt: time.Unix(reset.Int(), 0).UTC()}
		if m := w.Get("windowDurationMins"); m.Type == gjson.Number && m.Int() > 0 {
			win.Minutes = int(m.Int())
		}
		tool.Windows = append(tool.Windows, win)
	}
	if len(tool.Windows) == 0 {
		return tool, errors.New("Codex reported no rate-limit windows")
	}
	if plan := limits.Get("planType"); plan.Type == gjson.String {
		tool.Plan = plan.String()
	}
	return tool, nil
}

// ReadRateLimits starts `codexPath app-server`, performs the handshake, asks
// for the rate limits and stops the server. It gives up after 20 seconds. It
// prints nothing; on any error the caller keeps its previous reading.
func ReadRateLimits(ctx context.Context, codexPath string) (contract.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexPath, "app-server")
	cmd.Stderr = nil
	prepare(cmd)
	cmd.Cancel = func() error { return cmd.Process.Kill() }
	cmd.WaitDelay = 500 * time.Millisecond // an npm shim leaves a grandchild holding the pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return contract.Tool{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return contract.Tool{}, err
	}
	if err := cmd.Start(); err != nil {
		return contract.Tool{}, errors.New("could not start codex app-server")
	}
	stop := contain(cmd)
	defer func() {
		stop() // kills the whole process tree, shim children included
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()

	type reply struct {
		raw []byte
		err error
	}
	replies := make(chan reply, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case replies <- reply{raw: line}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case replies <- reply{err: io.EOF}:
		case <-ctx.Done():
		}
	}()

	// await returns the result of the response with the given id, skipping
	// notifications and anything else.
	await := func(id int) ([]byte, error) {
		for {
			select {
			case <-ctx.Done():
				return nil, errors.New("codex app-server timed out")
			case r := <-replies:
				if r.err != nil {
					return nil, errors.New("codex app-server closed early")
				}
				msg := gjson.ParseBytes(r.raw)
				if idv := msg.Get("id"); idv.Type != gjson.Number || int(idv.Int()) != id || msg.Get("method").Exists() {
					continue
				}
				if msg.Get("error").Exists() {
					return nil, errors.New("codex app-server returned an error")
				}
				res := msg.Get("result")
				if !res.Exists() {
					return nil, errors.New("codex app-server answered without a result")
				}
				return []byte(res.Raw), nil
			}
		}
	}
	send := func(v any) error {
		b, _ := json.Marshal(v)
		_, err := stdin.Write(append(b, '\n'))
		return err
	}

	init := map[string]any{"id": 0, "method": "initialize", "params": map[string]any{
		"clientInfo": map[string]string{"name": "devlets", "title": "Devlets", "version": ClientVersion},
	}}
	if err := send(init); err != nil {
		return contract.Tool{}, errors.New("could not talk to codex app-server")
	}
	if _, err := await(0); err != nil {
		return contract.Tool{}, err
	}
	if err := send(map[string]any{"method": "initialized"}); err != nil {
		return contract.Tool{}, errors.New("could not talk to codex app-server")
	}
	if err := send(map[string]any{"id": 1, "method": "account/rateLimits/read"}); err != nil {
		return contract.Tool{}, errors.New("could not talk to codex app-server")
	}
	res, err := await(1)
	if err != nil {
		return contract.Tool{}, err
	}
	tool, err := mapRateLimits(res)
	if err != nil {
		return contract.Tool{}, err
	}
	tool.ReadAt = time.Now().UTC()
	return tool, nil
}

// Refresh reads the Codex rate limits and, on success, stores them in
// st.Reading.Tools["codex"]. On any error st is left untouched, so the
// previous reading stays.
func Refresh(ctx context.Context, codexPath string, st *store.State) error {
	tool, err := ReadRateLimits(ctx, codexPath)
	if err != nil {
		return err
	}
	if st.Reading.Tools == nil {
		st.Reading.Tools = map[string]contract.Tool{}
	}
	st.Reading.Tools["codex"] = tool
	return nil
}
