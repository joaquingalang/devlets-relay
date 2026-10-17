// Command fakeappserver is a test stand-in for `codex app-server`: newline-
// delimited JSON-RPC over stdio. FAKE_CODEX_MODE picks the behaviour: ok,
// hang, error or newshape. FAKE_CODEX_RATELIMITS names a file whose "result"
// is used as the account/rateLimits/read result in mode ok.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

type msg struct {
	ID     *int   `json:"id"`
	Method string `json:"method"`
}

func send(v string) { fmt.Println(v) }

func main() {
	mode := os.Getenv("FAKE_CODEX_MODE")
	if mode == "" {
		mode = "ok"
	}
	switch mode {
	case "shim": // like the npm .cmd shim: start the real server as a child, then exit
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), "FAKE_CODEX_MODE=sleeper")
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		_ = c.Start()
		return
	case "sleeper":
		_ = os.WriteFile(os.Getenv("FAKE_CODEX_CHILDPID"), []byte(strconv.Itoa(os.Getpid())), 0o644)
		time.Sleep(time.Hour)
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue // notifications such as "initialized"
		}
		switch m.Method {
		case "initialize":
			send(`{"id":0,"method":"server/request"}`) // a server-initiated request, not our reply
			send(`{"method":"remoteControl/status/changed","params":{"status":"off"}}`)
			send(fmt.Sprintf(`{"id":%d,"result":{"userAgent":"fake/0","codexHome":"x","platformFamily":"unix","platformOs":"linux"}}`, *m.ID))
		case "account/rateLimits/read":
			switch mode {
			case "hang":
				time.Sleep(time.Hour)
			case "error":
				send(fmt.Sprintf(`{"id":%d,"error":{"code":-32000,"message":"not signed in"}}`, *m.ID))
			case "newshape":
				send(fmt.Sprintf(`{"id":%d,"result":{"rateLimits":{"windows":[{"pct":3}]}}}`, *m.ID))
			default:
				result := `{"rateLimits":{"primary":{"usedPercent":12.5,"windowDurationMins":300,"resetsAt":1794852360},"secondary":{"usedPercent":104.2,"windowDurationMins":10080,"resetsAt":1795000000},"planType":"plus"},"rateLimitsByLimitId":{"codex":{}},"accountId":"SECRET"}`
				if p := os.Getenv("FAKE_CODEX_RATELIMITS"); p != "" {
					b, _ := os.ReadFile(p)
					var v struct {
						Result json.RawMessage `json:"result"`
					}
					_ = json.Unmarshal(b, &v)
					var c bytes.Buffer
					_ = json.Compact(&c, v.Result)
					result = c.String()
				}
				send(`{"id":1,"method":"server/request"}`)
				send(`{"method":"account/rateLimits/updated","params":{}}`)
				time.Sleep(50 * time.Millisecond)
				send(fmt.Sprintf(`{"id":%d,"result":%s}`, *m.ID, result))
			}
		}
	}
}
