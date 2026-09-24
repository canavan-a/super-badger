// Command enhance regenerates internal/ui/splash.ans from art/superbadger.ans.
//
//	go run ./tools/enhance
package main

import (
	"flag"
	"fmt"
	"os"

	"superbadger-tui/internal/art"
)

func main() {
	in := flag.String("in", "art/superbadger.ans", "original art")
	out := flag.String("out", "internal/ui/splash.ans", "enhanced art to write")
	fur := flag.Float64("fur", art.DefaultOptions.FurAmount, "fur texture strength (0 = none)")
	sharp := flag.Float64("sharpen", art.DefaultOptions.Sharpen, "edge sharpening")
	flag.Parse()

	src, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res, err := art.Enhance(string(src), art.Options{Sharpen: *sharp, FurAmount: *fur})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(res), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}
