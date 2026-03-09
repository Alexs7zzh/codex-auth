package main

import (
	"context"
	"fmt"
	"os"

	"codex-auth/internal/app"
	"codex-auth/internal/quota"
	"codex-auth/internal/store"
	"codex-auth/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Printf("codex-auth %s\n\n", version)
			fmt.Println("Open the interactive Codex account switcher.")
			fmt.Println("")
			fmt.Println("Keys:")
			fmt.Println("  Up/Down or j/k  Move selection")
			fmt.Println("  Space           Mark switch target")
			fmt.Println("  Enter           Confirm switch, save, delete, or exit")
			fmt.Println("  e or i          Edit/save account name")
			fmt.Println("  d               Delete selected saved account")
			fmt.Println("  Esc or q        Close")
			return
		default:
			fmt.Fprintln(os.Stderr, "codex-auth does not support subcommands; run without arguments or use --help")
			os.Exit(1)
		}
	}

	home, err := store.ResolveHome("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve CODEX_HOME: %v\n", err)
		os.Exit(1)
	}

	runtime, accounts, warning, err := app.Load(home, quota.NewLiveProvider())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load accounts: %v\n", err)
		os.Exit(1)
	}

	model := tui.NewModel(runtime, accounts, warning)
	if err := tui.Run(context.Background(), model); err != nil {
		fmt.Fprintf(os.Stderr, "run tui: %v\n", err)
		os.Exit(1)
	}
}
