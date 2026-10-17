// Command helper is a stand-in for a user's previous status-line command.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sleep" {
		time.Sleep(3 * time.Second)
		fmt.Print("late")
		return
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	}
	_ = json.Unmarshal(raw, &in)
	fmt.Printf("PREV[%s]", in.Model.DisplayName)
}
