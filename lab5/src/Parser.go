package src

import (
	"fmt"
	"os"
	"strconv"
)


func parseArgs() int {
	if (len(os.Args) < 2) {
		fmt.Println("Invalid arguments : <Name app> <port server>")
		return -1
	}
	port, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Println("Invalid port server")
		return -1
	}
	return port
}