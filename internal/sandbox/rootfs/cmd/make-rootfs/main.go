// Command make-rootfs builds a minimal sandbox rootfs into the given directory.
//
//	go run ./internal/sandbox/rootfs/cmd/make-rootfs /path/to/rootfs
package main

import (
	"fmt"
	"os"

	"github.com/Muxcore-Media/core/internal/sandbox/rootfs"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <output-dir>\n", os.Args[0])
		os.Exit(2)
	}
	if err := rootfs.MakeRootfs(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "make-rootfs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(os.Args[1])
}
