package main

import (
	"fmt"
	"lab3/src/Control"
	"github.com/joho/godotenv"
	"lab3/cmd/config"
)

func init() {
	
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Failed load .env file")
    }

	config.Init()

}

func main() {

	Control.Execute()
	
}
