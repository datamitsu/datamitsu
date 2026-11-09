package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/datamitsu/datamitsu/internal/launch"
	"github.com/datamitsu/datamitsu/internal/ldflags"
)

var marker string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "payload" {
		fmt.Println(marker)
	}
	if code, handled := launch.Dispatch(); handled {
		os.Exit(code)
	}
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "make", "pause":
		if os.Args[1] == "pause" {
			path, err := launch.Path(os.Args[2], []string{"target", "argument with spaces"}, map[string]string{"LAUNCH_FIXTURE": "bound"})
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println(path)
			if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		path, err := launch.Ensure(os.Args[2], []string{"target", "argument with spaces"}, map[string]string{"LAUNCH_FIXTURE": "bound"})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s\n%s\n", ldflags.Version, path)
	case "target":
		fmt.Printf("%s|%s|%s\n", os.Args[2], os.Getenv("LAUNCH_FIXTURE"), os.Args[3])
	default:
		os.Exit(2)
	}
}
