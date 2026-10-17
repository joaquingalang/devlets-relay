// Package contract holds the reading the CLI sends to the phone. It mirrors
// contract/relay-reading.schema.json, which is copied from the Devlets repo.
package contract

import "time"

// Reading is the plaintext payload, encrypted before it leaves the computer.
type Reading struct {
	V        int             `json:"v"`
	Computer string          `json:"computer"`
	SentAt   time.Time       `json:"sentAt"`
	Hooked   []string        `json:"hooked"`
	Tools    map[string]Tool `json:"tools"`
}

// Tool is one coding tool's usage windows.
type Tool struct {
	Windows []Window  `json:"windows"`
	ReadAt  time.Time `json:"readAt"`
	Plan    string    `json:"plan,omitempty"`
}

// Window is one rate-limit window.
type Window struct {
	ID          string    `json:"id"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
	Minutes     int       `json:"minutes,omitempty"`
}
