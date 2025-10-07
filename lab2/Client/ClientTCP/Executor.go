package ClientTCP

import (
	"fmt"
	"os"
)

func Execute() {
	if len(os.Args) < 3 {
		fmt.Println("Invalid arguments")
		fmt.Println("<ip:port> <File path>")
	}
	client := NewClient(os.Args[1])
	err := client.Connect()
	if err != nil {
		return
	}
	defer client.Close()
	client.SendingFile(os.Args[2])
	client.receiveMessage()
}
