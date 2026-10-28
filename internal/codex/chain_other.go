//go:build !windows

package codex

// chainCheck always allows the chain here: arguments reach the program as they are.
func chainCheck(argv0 string) (Refusal, string) { return ChainOK, argv0 }
