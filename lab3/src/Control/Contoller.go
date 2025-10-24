package Control

import (
	"bufio"
	"fmt"
	"lab3/src/InterestingPlaces"
	"lab3/src/Search"
	"os"
	"strings"

)


func getAnswerUser() int {
	fmt.Printf("\nInput number : ")
	var number int
	_, err := fmt.Fscan(os.Stdin, &number)
	if err != nil {
		fmt.Println("Invalid number : ", err)
		getAnswerUser()
	}
	return number
}

func getNameLocationUser() string {
    reader := bufio.NewReader(os.Stdin)
    
    for {
        fmt.Printf("\nInput name location(exit for finish) : ")
        name, err := reader.ReadString('\n')
        if err != nil {
            fmt.Println("Invalid name : ", err)
            continue
        }
        
        name = strings.TrimSpace(name)
        
        if name != "" {
            return name
        }
    }
}

func Execute() {
	for {
		fmt.Printf("\n\n")
		name := getNameLocationUser()
		if name == "exit" {
			return
		}
		control(name)
	}
}

func control(namePlace string) {
	
	places, err := Search.SearchPlace(namePlace)
	if err != nil {
		fmt.Println("Failed to search locations")
		return
	}

	if len(places) == 0 {
		fmt.Println("Failed search locations")
		return
	}

	printLocations(places)

	number := getAnswerUser()
	if number > len(places) || number < 1 {
		return
	}

	place := places[number-1]

	request := NewRequest()

	weather, intPlaces := request.WaitResults(place.Point)

	if weather == nil {
		fmt.Println("Failed to get wheather")
	}

	printWeather(weather)

	if intPlaces == nil || len(intPlaces.Features) == 0 {
		fmt.Println("Failed to get interesting places")
		return
	}

	printIntPlaces(intPlaces)

	number = getAnswerUser()
	if number > len(intPlaces.Features) || number < 1 {
		return
	}

	xid := intPlaces.Features[number-1].Prop.Xid

	desc, err := InterestingPlaces.GetDescPlaces(xid)

	if err != nil {
		fmt.Println("Failed to get description interesting place")
		return
	}

	printDescription(desc)

}
