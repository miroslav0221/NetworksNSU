package main

import (
	"lab4/internal/controller"
)

func main() {
	ctrl := controller.NewController()
	ctrl.Start()
}
